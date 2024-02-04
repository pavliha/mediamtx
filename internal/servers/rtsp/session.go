package rtsp

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/auth"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/google/uuid"
	"github.com/pion/rtp"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/stream"
)

type session struct {
	isTLS       bool
	protocols   map[conf.Protocol]struct{}
	rsession    *gortsplib.ServerSession
	rconn       *gortsplib.ServerConn
	rserver     *gortsplib.Server
	pathManager defs.PathManager
	parent      *Server

	uuid            uuid.UUID
	created         time.Time
	path            defs.Path
	stream          *stream.Stream
	onUnreadHook    func()
	mutex           sync.Mutex
	state           gortsplib.ServerSessionState
	transport       *gortsplib.Transport
	pathName        string
	query           string
	decodeErrLogger logger.Writer
	writeErrLogger  logger.Writer
}

func (s *session) initialize() {
	s.uuid = uuid.New()
	s.created = time.Now()

	s.decodeErrLogger = logger.NewLimitedLogger(s)
	s.writeErrLogger = logger.NewLimitedLogger(s)

	s.Log(logger.Info, "created by %v", s.rconn.NetConn().RemoteAddr())
}

// Close closes a Session.
func (s *session) Close() {
	s.rsession.Close()
}

func (s *session) remoteAddr() net.Addr {
	return s.rconn.NetConn().RemoteAddr()
}

// Log implements logger.Writer.
func (s *session) Log(level logger.Level, format string, args ...interface{}) {
	id := hex.EncodeToString(s.uuid[:4])
	s.parent.Log(level, "[session %s] "+format, append([]interface{}{id}, args...)...)
}

// onClose is called by rtspServer.
func (s *session) onClose(err error) {
	if s.rsession.State() == gortsplib.ServerSessionStatePlay {
		s.onUnreadHook()
	}

	switch s.rsession.State() {
	case gortsplib.ServerSessionStatePrePlay, gortsplib.ServerSessionStatePlay:
		s.path.RemoveReader(defs.PathRemoveReaderReq{Author: s})

	case gortsplib.ServerSessionStatePreRecord, gortsplib.ServerSessionStateRecord:
		s.path.RemovePublisher(defs.PathRemovePublisherReq{Author: s})
	}

	s.path = nil
	s.stream = nil

	s.Log(logger.Info, "destroyed: %v", err)
}

// onAnnounce is called by rtspServer.
func (s *session) onAnnounce(c *conn, ctx *gortsplib.ServerHandlerOnAnnounceCtx) (*base.Response, error) {
	if len(ctx.Path) == 0 || ctx.Path[0] != '/' {
		return &base.Response{
			StatusCode: base.StatusBadRequest,
		}, fmt.Errorf("invalid path")
	}
	ctx.Path = ctx.Path[1:]

	if c.authNonce == "" {
		var err error
		c.authNonce, err = auth.GenerateNonce()
		if err != nil {
			return &base.Response{
				StatusCode: base.StatusInternalServerError,
			}, err
		}
	}

	res := s.pathManager.AddPublisher(defs.PathAddPublisherReq{
		Author: s,
		AccessRequest: defs.PathAccessRequest{
			Name:        ctx.Path,
			Query:       ctx.Query,
			Publish:     true,
			IP:          c.ip(),
			ID:          &c.uuid,
			RTSPRequest: ctx.Request,
			RTSPBaseURL: nil,
			RTSPNonce:   c.authNonce,
		},
	})

	if res.Err != nil {
		return &base.Response{
			StatusCode: base.StatusBadRequest,
		}, res.Err
	}

	s.path = res.Path

	s.mutex.Lock()
	s.state = gortsplib.ServerSessionStatePreRecord
	s.pathName = ctx.Path
	s.query = ctx.Query
	s.mutex.Unlock()

	return &base.Response{
		StatusCode: base.StatusOK,
	}, nil
}

// onSetup is called by rtspServer.
func (s *session) onSetup(c *conn, ctx *gortsplib.ServerHandlerOnSetupCtx,
) (*base.Response, *gortsplib.ServerStream, error) {
	if len(ctx.Path) == 0 || ctx.Path[0] != '/' {
		return &base.Response{
			StatusCode: base.StatusBadRequest,
		}, nil, fmt.Errorf("invalid path")
	}
	ctx.Path = ctx.Path[1:]

	// in case the client is setupping a stream with UDP or UDP-multicast, and these
	// transport protocols are disabled, gortsplib already blocks the request.
	// we have only to handle the case in which the transport protocol is TCP
	// and it is disabled.
	if ctx.Transport == gortsplib.TransportTCP {
		if _, ok := s.protocols[conf.Protocol(gortsplib.TransportTCP)]; !ok {
			return &base.Response{
				StatusCode: base.StatusUnsupportedTransport,
			}, nil, nil
		}
	}

	switch s.rsession.State() {
	case gortsplib.ServerSessionStateInitial, gortsplib.ServerSessionStatePrePlay: // play
		baseURL := &base.URL{
			Scheme:   ctx.Request.URL.Scheme,
			Host:     ctx.Request.URL.Host,
			Path:     ctx.Path,
			RawQuery: ctx.Query,
		}

		if ctx.Query != "" {
			baseURL.RawQuery += "/"
		} else {
			baseURL.Path += "/"
		}

		if c.authNonce == "" {
			var err error
			c.authNonce, err = auth.GenerateNonce()
			if err != nil {
				return &base.Response{
					StatusCode: base.StatusInternalServerError,
				}, nil, err
			}
		}

		res := s.pathManager.AddReader(defs.PathAddReaderReq{
			Author: s,
			AccessRequest: defs.PathAccessRequest{
				Name:        ctx.Path,
				Query:       ctx.Query,
				IP:          c.ip(),
				ID:          &c.uuid,
				RTSPRequest: ctx.Request,
				RTSPBaseURL: baseURL,
				RTSPNonce:   c.authNonce,
			},
		})

		if res.Err != nil {
			var terr2 defs.PathNoOnePublishingError
			if errors.As(res.Err, &terr2) {
				return &base.Response{
					StatusCode: base.StatusNotFound,
				}, nil, res.Err
			}

			return &base.Response{
				StatusCode: base.StatusBadRequest,
			}, nil, res.Err
		}

		s.path = res.Path
		s.stream = res.Stream

		s.mutex.Lock()
		s.state = gortsplib.ServerSessionStatePrePlay
		s.pathName = ctx.Path
		s.query = ctx.Query
		s.mutex.Unlock()

		var stream *gortsplib.ServerStream
		if !s.isTLS {
			stream = res.Stream.RTSPStream(s.rserver)
		} else {
			stream = res.Stream.RTSPSStream(s.rserver)
		}

		return &base.Response{
			StatusCode: base.StatusOK,
		}, stream, nil

	default: // record
		return &base.Response{
			StatusCode: base.StatusOK,
		}, nil, nil
	}
}

// onPlay is called by rtspServer.
func (s *session) onPlay(_ *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	h := make(base.Header)

	if s.rsession.State() == gortsplib.ServerSessionStatePrePlay {
		s.Log(logger.Info, "is reading from path '%s', with %s, %s",
			s.path.Name(),
			s.rsession.SetuppedTransport(),
			defs.MediasInfo(s.rsession.SetuppedMedias()))

		s.mutex.Lock()
		s.state = gortsplib.ServerSessionStatePlay
		s.transport = s.rsession.SetuppedTransport()
		s.mutex.Unlock()
	}

	return &base.Response{
		StatusCode: base.StatusOK,
		Header:     h,
	}, nil
}

// onRecord is called by rtspServer.
func (s *session) onRecord(_ *gortsplib.ServerHandlerOnRecordCtx) (*base.Response, error) {
	res := s.path.StartPublisher(defs.PathStartPublisherReq{
		Author:             s,
		Desc:               s.rsession.AnnouncedDescription(),
		GenerateRTPPackets: false,
	})
	if res.Err != nil {
		return &base.Response{
			StatusCode: base.StatusBadRequest,
		}, res.Err
	}

	s.stream = res.Stream

	for _, medi := range s.rsession.AnnouncedDescription().Medias {
		for _, forma := range medi.Formats {
			cmedi := medi
			cforma := forma

			s.rsession.OnPacketRTP(cmedi, cforma, func(pkt *rtp.Packet) {
				pts, ok := s.rsession.PacketPTS(cmedi, pkt)
				if !ok {
					return
				}

				res.Stream.WriteRTPPacket(cmedi, cforma, pkt, time.Now(), pts)
			})
		}
	}

	s.mutex.Lock()
	s.state = gortsplib.ServerSessionStateRecord
	s.transport = s.rsession.SetuppedTransport()
	s.mutex.Unlock()

	return &base.Response{
		StatusCode: base.StatusOK,
	}, nil
}

// onPause is called by rtspServer.
func (s *session) onPause(_ *gortsplib.ServerHandlerOnPauseCtx) (*base.Response, error) {
	switch s.rsession.State() {
	case gortsplib.ServerSessionStatePlay:
		s.onUnreadHook()

		s.mutex.Lock()
		s.state = gortsplib.ServerSessionStatePrePlay
		s.mutex.Unlock()

	case gortsplib.ServerSessionStateRecord:
		s.path.StopPublisher(defs.PathStopPublisherReq{Author: s})

		s.mutex.Lock()
		s.state = gortsplib.ServerSessionStatePreRecord
		s.mutex.Unlock()
	}

	return &base.Response{
		StatusCode: base.StatusOK,
	}, nil
}
