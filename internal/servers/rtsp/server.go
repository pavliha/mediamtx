// Package rtsp contains a RTSP server.
package rtsp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
)

// ErrConnNotFound is returned when a connection is not found.
var ErrConnNotFound = errors.New("connection not found")

// ErrSessionNotFound is returned when a session is not found.
var ErrSessionNotFound = errors.New("session not found")

func printAddresses(srv *gortsplib.Server) string {
	var ret []string

	ret = append(ret, fmt.Sprintf("%s (TCP)", srv.RTSPAddress))

	if srv.UDPRTPAddress != "" {
		ret = append(ret, fmt.Sprintf("%s (UDP/RTP)", srv.UDPRTPAddress))
	}

	if srv.UDPRTCPAddress != "" {
		ret = append(ret, fmt.Sprintf("%s (UDP/RTCP)", srv.UDPRTCPAddress))
	}

	return strings.Join(ret, ", ")
}

type serverParent interface {
	logger.Writer
}

// Server is a RTSP server.
type Server struct {
	Address           string
	ReadTimeout       conf.StringDuration
	WriteTimeout      conf.StringDuration
	WriteQueueSize    int
	UseUDP            bool
	UseMulticast      bool
	RTPAddress        string
	RTCPAddress       string
	MulticastIPRange  string
	MulticastRTPPort  int
	MulticastRTCPPort int
	RTSPAddress       string
	Protocols         map[conf.Protocol]struct{}
	PathManager       defs.PathManager
	Parent            serverParent

	ctx       context.Context
	ctxCancel func()
	wg        sync.WaitGroup
	srv       *gortsplib.Server
	conns     map[*gortsplib.ServerConn]*conn
	sessions  map[*gortsplib.ServerSession]*session
}

// Initialize initializes the server.
func (s *Server) Initialize() error {
	s.ctx, s.ctxCancel = context.WithCancel(context.Background())

	s.conns = make(map[*gortsplib.ServerConn]*conn)
	s.sessions = make(map[*gortsplib.ServerSession]*session)

	s.srv = &gortsplib.Server{
		Handler:        s,
		ReadTimeout:    time.Duration(s.ReadTimeout),
		WriteTimeout:   time.Duration(s.WriteTimeout),
		WriteQueueSize: s.WriteQueueSize,
		RTSPAddress:    s.Address,
	}

	if s.UseUDP {
		s.srv.UDPRTPAddress = s.RTPAddress
		s.srv.UDPRTCPAddress = s.RTCPAddress
	}

	if s.UseMulticast {
		s.srv.MulticastIPRange = s.MulticastIPRange
		s.srv.MulticastRTPPort = s.MulticastRTPPort
		s.srv.MulticastRTCPPort = s.MulticastRTCPPort
	}

	err := s.srv.Start()
	if err != nil {
		return err
	}

	s.Log(logger.Info, "listener opened on %s", printAddresses(s.srv))

	s.wg.Add(1)
	go s.run()

	return nil
}

// Log implements logger.Writer.
func (s *Server) Log(level logger.Level, format string, args ...interface{}) {
	s.Parent.Log(level, "[%s] "+format, append([]interface{}{"RTSP"}, args...)...)
}

// Close closes the server.
func (s *Server) Close() {
	s.Log(logger.Info, "listener is closing")
	s.ctxCancel()
	s.wg.Wait()
}

func (s *Server) run() {
	defer s.wg.Done()

	serverErr := make(chan error)
	go func() {
		serverErr <- s.srv.Wait()
	}()

outer:
	select {
	case err := <-serverErr:
		s.Log(logger.Error, "%s", err)
		break outer

	case <-s.ctx.Done():
		s.srv.Close()
		<-serverErr
		break outer
	}

	s.ctxCancel()
}
