// Package rtsp contains the RTSP static source.
package rtsp

import (
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/pion/rtp"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
)

// Source is a RTSP static source.
type Source struct {
	Parent defs.StaticSourceParent
}

// Log implements logger.Writer.
func (s *Source) Log(level logger.Level, format string, args ...interface{}) {
	s.Parent.Log(level, "[RTSP source] "+format, args...)
}

// Run implements StaticSource.
func (s *Source) Run(params defs.StaticSourceRunParams) error {
	s.Log(logger.Debug, "connecting")

	decodeErrLogger := logger.NewLimitedLogger(s)

	c := &gortsplib.Client{
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   5 * time.Second,
		WriteQueueSize: 512,
		OnRequest: func(req *base.Request) {
			s.Log(logger.Debug, "[c->s] %v", req)
		},
		OnResponse: func(res *base.Response) {
			s.Log(logger.Debug, "[s->c] %v", res)
		},
		OnTransportSwitch: func(err error) {
			s.Log(logger.Warn, err.Error())
		},
		OnPacketLost: func(err error) {
			decodeErrLogger.Log(logger.Warn, err.Error())
		},
		OnDecodeError: func(err error) {
			decodeErrLogger.Log(logger.Warn, err.Error())
		},
	}

	u, err := base.ParseURL("rtsp://192.168.2.119:554")
	if err != nil {
		return err
	}

	err = c.Start(u.Scheme, u.Host)
	if err != nil {
		return err
	}
	defer c.Close()

	readErr := make(chan error)
	go func() {
		readErr <- func() error {
			desc, _, err := c.Describe(u)
			if err != nil {
				return err
			}

			err = c.SetupAll(desc.BaseURL, desc.Medias)
			if err != nil {
				return err
			}

			res := s.Parent.SetReady(defs.PathSourceStaticSetReadyReq{
				Desc:               desc,
				GenerateRTPPackets: false,
			})
			if res.Err != nil {
				return res.Err
			}

			defer s.Parent.SetNotReady(defs.PathSourceStaticSetNotReadyReq{})

			for _, medi := range desc.Medias {
				for _, forma := range medi.Formats {
					cmedi := medi
					cforma := forma

					c.OnPacketRTP(cmedi, cforma, func(pkt *rtp.Packet) {
						pts, ok := c.PacketPTS(cmedi, pkt)
						if !ok {
							return
						}

						res.Stream.WriteRTPPacket(cmedi, cforma, pkt, time.Now(), pts)
					})
				}
			}

			if err != nil {
				return err
			}

			_, err = c.Play(nil)
			if err != nil {
				return err
			}

			return c.Wait()
		}()
	}()

	for {
		select {
		case err := <-readErr:
			return err

		case <-params.Context.Done():
			c.Close()
			<-readErr
			return nil
		}
	}
}
