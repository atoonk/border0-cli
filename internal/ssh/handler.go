package ssh

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"

	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
	border0_http "github.com/borderzero/border0-cli/internal/http"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/net/proxy"

	gssh "github.com/gliderlabs/ssh"
)

const (
	defaultTimeout = 30 * time.Second
)

var (
	defaultKeyFiles       = []string{"id_dsa", "id_ecdsa", "id_ed25519", "id_rsa"}
	ErrFailedToGetSshCert = errors.New("failed to get ssh cert")
)

type httpProxy struct {
	host     string
	haveAuth bool
	username string
	password string
	forward  proxy.Dialer
}

func sshServer() string {
	if os.Getenv("BORDER0_TUNNEL") != "" {
		return os.Getenv("BORDER0_TUNNEL")
	} else {
		return "tunnel.border0.com"
	}
}

func getSshCert(userId string, socketID string, accessToken string, numOfRetry int) (s ssh.Signer, err error) {

	home, err := os.UserHomeDir()
	if err != nil {
		return s, fmt.Errorf("error: failed to get home dir: %w", err)
	}

	privateKeyFile := home + "/.border0"
	if _, err := os.Stat(privateKeyFile); os.IsNotExist(err) {
		err := os.Mkdir(privateKeyFile, 0700)
		if err != nil {
			return s, fmt.Errorf("error: could not create directory: %w", err)
		}
	}

	privateKeyFile = home + "/.border0/user_" + userId

	if _, err := os.Stat(privateKeyFile); os.IsNotExist(err) {
		// Let's create a key pair

		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return s, fmt.Errorf("error: failed to create ssh key: %v", err)
		}

		parsed, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return s, fmt.Errorf("error: failed to create ssh key: %v", err)
		}

		keyPem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: parsed})
		err = ioutil.WriteFile(privateKeyFile, keyPem, 0600)
		if err != nil {
			return s, fmt.Errorf("error: failed to write ssh key: %v", err)
		}
	}
	// Ok now let's load the key

	if _, err := os.Stat(privateKeyFile); os.IsNotExist(err) {
		return s, fmt.Errorf("error: failed to load private ssh key: %v", err)
	}
	// read private key from file
	keyContent, _ := ioutil.ReadFile(privateKeyFile)
	if err != nil {
		return s, fmt.Errorf("error: failed to load private ssh key: %v", err)
	}

	block, _ := pem.Decode(keyContent)
	if block == nil {
		return s, fmt.Errorf("failed to decode PEM block containing public key: %v", err)
	}

	pkey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return s, fmt.Errorf("error: failed to parse private ssh key: %v", err)
	}

	// create public key
	pub, err := ssh.NewPublicKey(&pkey.PublicKey)
	if err != nil {
		return s, fmt.Errorf("error: failed to create public ssh key: %v", err)
	}
	data := ssh.MarshalAuthorizedKey(pub)

	//post signing request
	signedCert := models.SshCsr{}
	newCsr := &models.SshCsr{
		SSHPublicKey: strings.TrimRight(string(data), "\n"),
	}

	client, err := border0_http.NewClientWithAccessToken(accessToken)
	if err != nil {
		return s, fmt.Errorf("error: %v", err)
	}

	for i := 1; i <= numOfRetry; i++ {
		err = client.Request("POST", "socket/"+socketID+"/signkey", &signedCert, newCsr)
		if err == nil {
			break
		}
		log.Printf("Unable to get signed cert from API, will try again in %d seconds. Attempt %d of 10\n", 2*i, i)

		d := time.Duration(2*i) * time.Second
		time.Sleep(d)
	}
	if signedCert.SSHSignedCert == "" {
		return s, fmt.Errorf("error: Unable to get signed key from Server")
	}

	certData := []byte(signedCert.SSHSignedCert)
	pubcert, _, _, _, err := ssh.ParseAuthorizedKey(certData)
	if err != nil {
		return s, fmt.Errorf("error: %v", err)
	}
	cert, ok := pubcert.(*ssh.Certificate)
	if !ok {
		return s, fmt.Errorf("error failed to cast to certificate: %v", err)
	}

	clientKey, _ := ssh.ParsePrivateKey(keyContent)
	certSigner, err := ssh.NewCertSigner(cert, clientKey)
	if err != nil {
		return s, fmt.Errorf("NewCertSigner: %v", err)
	}

	return certSigner, nil
}

func SshConnect(userID string, socketID string, tunnelID string, port int, targethost string, identityFile string, proxyHost string, version string, localhttp, localssh bool, sshCa string, accessToken, httpdir string, connectorAuthRequired bool, caCertPool *x509.CertPool) error {
	var tunnel *models.Tunnel
	var err error

	if tunnelID != "" {
		tunnel, err = api.NewAPI(api.WithCredentials(models.NewCredentials(accessToken, models.CredentialsTypeUser))).GetTunnel(context.Background(), socketID, tunnelID)
		if err != nil {
			return fmt.Errorf("error getting tunnel: %v", err)
		}
	}

	sshConfig := &ssh.ClientConfig{
		User:            userID,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         defaultTimeout,
		ClientVersion:   fmt.Sprintf("SSH-2.0-Border0-%s", version),
	}
	var keyFiles []string
	var signers []ssh.Signer

	if identityFile != "" {
		f := []string{identityFile}
		if auth, err := authWithPrivateKeys(f, true); err == nil {
			signers = append(signers, auth...)
		}
	}

	if auth, err := authWithAgent(); err == nil {
		signers = append(signers, auth...)
	}

	home, err := os.UserHomeDir()
	if err == nil {
		for _, k := range defaultKeyFiles {
			f := home + "/.ssh/" + k
			if _, err := os.Stat(f); err == nil {
				keyFiles = append(keyFiles, f)
			}
		}
	}

	if auth, err := authWithPrivateKeys(keyFiles, false); err == nil {
		signers = append(signers, auth...)
	}

	// Start a thread that refreshes the token
	// refresh every hour, 3600secs
	go func() {
		for {
			time.Sleep(3600 * time.Second)
			_, err := border0_http.RefreshLogin()
			if err != nil {
				fmt.Println(err)
			}
		}
	}()

	proxyMatch, _ := regexp.Compile("^http(s)?://")
	var proxyDialer proxy.Dialer
	if proxyMatch.MatchString(proxyHost) {
		proxyURL, err := url.Parse(proxyHost)
		if err != nil {
			log.Fatalf("Invalid proxy URL: %s", err)
		}
		proxy.RegisterDialerType("http", newHttpProxy)
		proxy.RegisterDialerType("https", newHttpProxy)
		proxyDialer, _ = proxy.FromURL(proxyURL, proxy.Direct)
	} else {
		proxyDialer = proxy.Direct
	}

	for {
		// Let's fetch a short lived signed cert from api.border0.com
		// We'll use that to authenticate. This returns a signer object.
		// for now we'll just add it to the signers list.
		// In future, this is the only auth method we should use.
		sshCert, err := getSshCert(userID, socketID, accessToken, 10)
		if err != nil {
			return ErrFailedToGetSshCert
		}
		// If we got a cert, we use that for auth method. Otherwise use static keys
		if sshCert != nil {
			sshConfig.Auth = []ssh.AuthMethod{ssh.PublicKeys(sshCert)}
		} else if signers != nil {
			sshConfig.Auth = []ssh.AuthMethod{ssh.PublicKeys(signers...)}
		} else {
			return errors.New("no ssh keys found for authenticating")
		}

		fmt.Println("\nConnecting to Server: " + sshServer() + "\n")
		time.Sleep(1 * time.Second)

		sshConnect(proxyDialer, sshConfig, tunnel, port, targethost, localhttp, localssh, sshCa, httpdir, connectorAuthRequired, caCertPool, socketID)
	}
}

func sshConnect(proxyDialer proxy.Dialer, sshConfig *ssh.ClientConfig, tunnel *models.Tunnel, port int, targethost string, localhttp, localssh bool, sshCa, httpDir string, connectorAuthRequired bool, caCertPool *x509.CertPool, socketID string) {
	remoteHost := net.JoinHostPort(sshServer(), "22")

	conn, err := proxyDialer.Dial("tcp", remoteHost)
	if err != nil {
		log.Printf("Dial INTO remote server error: %s", err)
		return
	}

	defer conn.Close()

	sshCon, channel, req, err := ssh.NewClientConn(conn, remoteHost, sshConfig)
	if err != nil {
		log.Printf("Dial INTO remote server error: %s", err)
		return
	}
	defer sshCon.Close()

	sshClient := ssh.NewClient(sshCon, channel, req)
	defer sshClient.Close()

	done := make(chan bool, 1)
	defer func() { done <- true }()
	go KeepAlive(sshClient, done)

	var listenPort int
	if tunnel == nil {
		listenPort = 0
	} else {
		listenPort = tunnel.LocalPort
	}

	listener, err := sshClient.Listen("tcp", fmt.Sprintf("localhost:%d", listenPort))
	if err != nil {
		log.Printf("Listen open port ON remote server on port %d error: %s", listenPort, err)
		return
	}
	defer listener.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		log.Printf("Failed to create session: %v", err)
		return
	}
	defer session.Close()

	session.Stdout = os.Stdout
	modes := ssh.TerminalModes{}

	if err := session.RequestPty("xterm-256color", 80, 40, modes); err != nil {
		log.Printf("request for pseudo terminal failed: %s", err)
		return
	}

	if err := session.Shell(); err != nil {
		log.Print(err)
		return
	}

	var tlsConfig *tls.Config

	if connectorAuthRequired {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			log.Printf("Failed to generate ECDSA key: %s\n", err)
			return
		}

		keyDer, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			log.Printf("Failed to serialize ECDSA key: %s\n", err)
			return
		}

		template := x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject: pkix.Name{
				CommonName:   socketID,
				Organization: []string{"Border0 Connector"},
			},
			IsCA:                  true,
			NotBefore:             time.Now().Add(-time.Hour * 24 * 365),
			NotAfter:              time.Now().Add(time.Hour * 24 * 365 * 10),
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			BasicConstraintsValid: true,
			DNSNames:              []string{socketID},
		}

		certDer, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			log.Printf("Failed to create certificate: %s\n", err)
			return
		}

		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDer})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})

		cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			log.Printf("Failed to create certificate: %s\n", err)
			return
		}

		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    caCertPool,
		}
	}

	var sshServer *gssh.Server
	if localssh {
		sshServer = newServer(sshCa)
	}

	if localhttp {
		go border0_http.StartLocalHTTPServer(httpDir, listener)
	} else {
		go func() {
			for {
				client, err := listener.Accept()
				if err != nil {
					log.Printf("Tunnel Connection accept error: %v", err)
					return
				}

				go func() {
					if connectorAuthRequired {
						tlsConn := tls.Server(client, tlsConfig)
						if err = tlsConn.Handshake(); err != nil {
							log.Printf("client tls handshake failed: %s", err)
							return
						}

						_, err = client.Write([]byte("BORDER0-CLIENT-CONNECTOR-AUTHENTICATED"))
						if err != nil {
							log.Printf("Failed to complete handshake: %s", err)
							return
						}
						log.Printf("client %s authenticated", tlsConn.ConnectionState().PeerCertificates[0].Subject.CommonName)
						time.Sleep(200 * time.Millisecond)
					}

					if localssh {
						go sshServer.HandleConn(client)
					} else {
						local, err := net.Dial("tcp", fmt.Sprintf("%s:%d", targethost, port))
						if err != nil {
							log.Printf("Dial INTO local service error: %s", err)
							return
						}

						go handleClient(client, local)
					}
				}()
			}
		}()
	}

	if err := session.Wait(); err != nil {
		log.Printf("ssh session error: %v", err)
	}
}

func KeepAlive(sshClient *ssh.Client, done chan bool) {
	t := time.NewTicker(10 * time.Second)
	max := 4
	n := 0

	defer t.Stop()

	for {
		select {
		case <-done:
			return
		case <-t.C:
			aliveChan := make(chan bool, 1)

			go func() {
				_, _, err := sshClient.SendRequest("keepalive@openssh.com", true, nil)
				if err != nil {
					aliveChan <- false
				} else {
					aliveChan <- true
				}
			}()

			select {
			case <-time.After(5 * time.Second):
				n++
			case alive := <-aliveChan:
				if !alive {
					n++
				} else {
					n = 0
				}
			}

			if n >= max {
				log.Println("ssh keepalive timeout, disconnecting")
				sshClient.Close()
				return
			}
		}
	}
}

func handleClient(client net.Conn, remote net.Conn) {
	defer client.Close()
	defer remote.Close()

	chDone := make(chan bool, 1)

	// Start remote -> local data transfer
	go func() {
		io.Copy(client, remote)
		chDone <- true
	}()

	// Start local -> remote data transfer
	go func() {
		io.Copy(remote, client)
		chDone <- true
	}()

	<-chDone
}

func authWithPrivateKeys(keyFiles []string, fatalOnError bool) ([]ssh.Signer, error) {
	var signers []ssh.Signer

	for _, file := range keyFiles {

		b, err := ioutil.ReadFile(file)
		if err != nil {
			if fatalOnError {
				log.Fatalf("Cannot read SSH key file %s (%v)\n", file, err.Error())
			} else {
				continue
			}
		}
		signer, err := ssh.ParsePrivateKey(b)
		if err != nil {
			if fatalOnError {
				log.Fatalf("Cannot read SSH key file %s (%v)\n", file, err.Error())
			} else {
				continue
			}
		}
		signers = append(signers, signer)
	}

	return signers, nil
}

func authWithAgent() ([]ssh.Signer, error) {
	if os.Getenv("SSH_AUTH_SOCK") != "" {
		sshAgent, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
		if err == nil {
			agentSigners, _ := agent.NewClient(sshAgent).Signers()
			return agentSigners, nil
		}
	}

	return nil, nil
}

func newHttpProxy(uri *url.URL, forward proxy.Dialer) (proxy.Dialer, error) {
	s := new(httpProxy)
	s.host = uri.Host
	s.forward = forward
	if uri.User != nil {
		s.haveAuth = true
		s.username = uri.User.Username()
		s.password, _ = uri.User.Password()
	}
	return s, nil
}

func (s *httpProxy) Dial(network, addr string) (net.Conn, error) {
	c, err := s.forward.Dial("tcp", s.host)
	if err != nil {
		return nil, err
	}

	reqURL, err := url.Parse("http://" + addr)
	if err != nil {
		c.Close()
		return nil, err
	}
	reqURL.Scheme = ""

	req, err := http.NewRequest("CONNECT", reqURL.String(), nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	req.Close = false
	if s.haveAuth {
		req.SetBasicAuth(s.username, s.password)
	}
	req.Header.Set("User-Agent", "Border0")

	err = req.Write(c)
	if err != nil {
		c.Close()
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(c), req)
	if err != nil {
		resp.Body.Close()
		c.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		c.Close()
		err = fmt.Errorf("connect server using proxy error, StatusCode [%d]", resp.StatusCode)
		return nil, err
	}

	return c, nil
}
