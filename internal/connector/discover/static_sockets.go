package discover

import (
	"context"
	"reflect"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/connector/config"
)

type StaticSocketFinder struct{}

var _ Discover = (*StaticSocketFinder)(nil)

func (s *StaticSocketFinder) SkipRun(ctx context.Context, cfg config.Config, state DiscoverState) bool {
	return false
}

func (s *StaticSocketFinder) WaitSeconds() int64 {
	return 30
}

func (s *StaticSocketFinder) Find(ctx context.Context, cfg config.Config, state DiscoverState) ([]models.Socket, error) {
	sockets := []models.Socket{}
	for _, socketMap := range cfg.Sockets {
		socket := models.Socket{}

		for k, v := range socketMap {
			socket.Name = k
			socket.AllowedEmailAddresses = v.AllowedEmailAddresses
			socket.AllowedEmailDomains = v.AllowedEmailDomains
			socket.SocketType = v.Type
			socket.UpstreamUsername = v.UpstreamUser
			socket.UpstreamPassword = v.UpstreamPassword
			socket.TargetHostname = v.Host
			socket.TargetPort = v.Port
			socket.ConnectorAuthenticationEnabled = v.ConnectorAuthenticationEnabled
			socket.UpstreamHttpHostname = v.UpstreamHttpHostname
			socket.CloudAuthEnabled = true
			socket.PolicyNames = v.Policies

			socket.UpstreamType = v.UpstreamType
		}

		sockets = append(sockets, socket)
	}

	return sockets, nil
}

func (s *StaticSocketFinder) Name() string {
	return reflect.TypeOf(s).Elem().Name()
}
