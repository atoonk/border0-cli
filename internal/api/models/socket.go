package models

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	tagKeyManagedBy = "managed_by"
)

type ConnectorData struct {
	Name           string
	Connector      string
	Type           string
	Port           int
	TargetHostname string
	PolicyGroup    string
	Ec2Tag         string
	InstanceId     string
	PluginName     string
	ManagedBy      string
}

func (c *ConnectorData) Tags() map[string]string {
	data := map[string]string{
		"name":            c.Name,
		"connector_name":  c.Connector,
		"type":            c.Type,
		"target_port":     strconv.Itoa(c.Port),
		"target_hostname": c.TargetHostname,
		"ec2_tag":         c.Ec2Tag,
		"policy_group":    c.PolicyGroup,
		"instance_id":     c.InstanceId,
		"plugin_name":     c.PluginName,
	}

	if c.ManagedBy != "" {
		data[tagKeyManagedBy] = c.ManagedBy
	}

	return data
}

func (c *ConnectorData) Key() string {
	if c.Name == "" && c.Connector == "" && c.Type == "" && c.Port == 0 {
		return ""
	}

	return fmt.Sprintf("%v;%v;%v", c.Name, c.Connector, c.PluginName)
}

type Socket struct {
	Tunnels                        []Tunnel          `json:"tunnels,omitempty"`
	Username                       string            `json:"user_name,omitempty"`
	SocketID                       string            `json:"socket_id,omitempty"`
	SocketTcpPorts                 []int             `json:"socket_tcp_ports,omitempty"`
	Dnsname                        string            `json:"dnsname,omitempty"`
	Name                           string            `json:"name,omitempty"`
	Description                    string            `json:"description,omitempty"`
	SocketType                     string            `json:"socket_type,omitempty"`
	ProtectedSocket                bool              `json:"protected_socket"`
	ProtectedUsername              string            `json:"protected_username"`
	ProtectedPassword              string            `json:"protected_password"`
	AllowedEmailAddresses          []string          `json:"cloud_authentication_email_allowed_addressses,omitempty"`
	AllowedEmailDomains            []string          `json:"cloud_authentication_email_allowed_domains,omitempty"`
	SSHCa                          string            `json:"ssh_ca,omitempty"`
	UpstreamUsername               string            `json:"upstream_username,omitempty"`
	UpstreamPassword               string            `json:"upstream_password,omitempty"`
	UpstreamCert                   *string           `json:"upstream_cert,omitempty"`
	UpstreamKey                    *string           `json:"upstream_key,omitempty"`
	UpstreamCa                     *string           `json:"upstream_ca,omitempty"`
	UpstreamHttpHostname           string            `json:"upstream_http_hostname,omitempty"`
	UpstreamType                   string            `json:"upstream_type,omitempty"`
	CloudAuthEnabled               bool              `json:"cloud_authentication_enabled,omitempty"`
	ConnectorAuthenticationEnabled bool              `json:"connector_authentication_enabled,omitempty"`
	Tags                           map[string]string `json:"tags,omitempty"`
	CustomDomains                  []string          `json:"custom_domains,omitempty"`
	PolicyNames                    []string          `json:"policy_names,omitempty"`
	Policies                       []Policy          `json:"policies,omitempty"`
	OrgCustomDomain                string            `json:"org_custom_domain,omitempty"`

	TargetHostname string         `json:"-"`
	TargetPort     int            `json:"-"`
	PolicyGroup    string         `json:"-"`
	Ec2Tag         string         `json:"-"`
	InstanceId     string         `json:"-"`
	PluginName     string         `json:"-"`
	ManagedBy      string         `json:"-"`
	ConnectorData  *ConnectorData `json:"-"`
}

func (s *Socket) SanitizeName() {
	socketName := strings.Replace(s.Name, ".", "-", -1)
	socketName = strings.Replace(socketName, " ", "-", -1)
	socketName = strings.Replace(socketName, ".", "-", -1)
	s.Name = strings.Replace(socketName, "_", "-", -1)
}

func (s *Socket) BuildConnectorData(connectorName, principal string) {
	s.ConnectorData = &ConnectorData{
		Name:           s.Name,
		Connector:      connectorName,
		Type:           s.SocketType,
		Port:           s.TargetPort,
		TargetHostname: s.TargetHostname,
		PolicyGroup:    s.PolicyGroup,
		Ec2Tag:         s.Ec2Tag,
		InstanceId:     s.InstanceId,
		PluginName:     s.PluginName,
		ManagedBy:      principal,
	}
}

func (s *Socket) BuildConnectorDataAndTags(connectorName, principal string) {
	s.BuildConnectorData(connectorName, principal)
	s.Tags = s.ConnectorData.Tags()
}

func (s *Socket) BuildConnectorDataByTags() {
	data := ConnectorData{}
	s.ConnectorData = &data

	if len(s.Tags) == 0 {
		return
	}

	port, _ := strconv.Atoi(s.Tags["target_port"])
	data.Name = s.Tags["name"]
	data.Connector = s.Tags["connector_name"]
	data.Type = s.Tags["type"]
	data.Port = port
	data.TargetHostname = s.Tags["target_hostname"]
	data.Ec2Tag = s.Tags["ec2_tag"]
	data.InstanceId = s.Tags["instance_id"]
	data.PolicyGroup = s.Tags["policy_group"]
	data.PluginName = s.Tags["plugin_name"]
	data.ManagedBy = s.Tags[tagKeyManagedBy]

	s.ConnectorData = &data
}

func (s *Socket) SetupTypeAndUpstreamTypeByPortOrTags() {
	if s.UpstreamType == "" {
		s.UpstreamType = "http"

		if s.SocketType != "" {
			switch s.SocketType {
			case "mysql":
				s.UpstreamType = "mysql"
				s.SocketType = "database"
			case "postgres":
				s.UpstreamType = "postgres"
				s.SocketType = "database"
			case "database":
				if s.TargetPort == 3306 {
					s.UpstreamType = "mysql"
				}
				if s.TargetPort == 5432 {
					s.UpstreamType = "postgres"
				}
			case "https":
				s.SocketType = "http"
				s.UpstreamType = "https"
			}

		} else {
			switch s.TargetPort {
			case 3306:
				s.SocketType = "database"
				s.UpstreamType = "mysql"
			case 5432:
				s.SocketType = "database"
				s.UpstreamType = "postgres"
			case 22:
				s.SocketType = "ssh"
			case 80:
				s.SocketType = "http"
			case 443:
				s.SocketType = "http"
				s.UpstreamType = "https"
			}
		}
	}
}

type Tunnel struct {
	TunnelID     string `json:"tunnel_id,omitempty"`
	LocalPort    int    `json:"local_port,omitempty"`
	TunnelServer string `json:"tunnel_server,omitempty"`
}
