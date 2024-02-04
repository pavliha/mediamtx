package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	rtspsource "github.com/bluenviron/mediamtx/internal/staticsources/rtsp"
	"github.com/sirupsen/logrus"
)

const (
	staticSourceHandlerRetryPause = 5 * time.Second
)

type staticSourceHandlerParent interface {
	staticSourceHandlerSetReady(context.Context, defs.PathSourceStaticSetReadyReq)
	staticSourceHandlerSetNotReady(context.Context, defs.PathSourceStaticSetNotReadyReq)
}

// staticSourceHandler is a static source handler.
type staticSourceHandler struct {
	conf           *conf.Path
	logLevel       conf.LogLevel
	readTimeout    conf.StringDuration
	writeTimeout   conf.StringDuration
	resolvedSource string
	parent         staticSourceHandlerParent

	ctx       context.Context
	ctxCancel func()
	instance  defs.StaticSource
	running   bool

	// in
	chInstanceSetReady    chan defs.PathSourceStaticSetReadyReq
	chInstanceSetNotReady chan defs.PathSourceStaticSetNotReadyReq

	// out
	done chan struct{}
}

func (s *staticSourceHandler) initialize() {
	s.chInstanceSetReady = make(chan defs.PathSourceStaticSetReadyReq)
	s.chInstanceSetNotReady = make(chan defs.PathSourceStaticSetNotReadyReq)

	switch {
	case strings.HasPrefix(s.resolvedSource, "rtsp://"):
		s.instance = &rtspsource.Source{
			Parent: s,
		}
	}
}

func (s *staticSourceHandler) close(reason string) {
	s.stop(reason)
}

func (s *staticSourceHandler) start(onDemand bool) {
	if s.running {
		panic("should not happen")
	}

	s.running = true
	logrus.Info("[source] started")
	s.ctx, s.ctxCancel = context.WithCancel(context.Background())
	s.done = make(chan struct{})

	go s.run()
}

func (s *staticSourceHandler) stop(reason string) {
	if !s.running {
		panic("should not happen")
	}

	s.running = false
	logrus.Info("[source] stopped ", reason)

	s.ctxCancel()

	// we must wait since s.ctx is not thread safe
	<-s.done
}

func (s *staticSourceHandler) run() {
	defer close(s.done)

	var runCtx context.Context
	var runCtxCancel func()
	runErr := make(chan error)

	recreate := func() {
		runCtx, runCtxCancel = context.WithCancel(context.Background())
		go func() {
			runErr <- s.instance.Run(defs.StaticSourceRunParams{
				Context: runCtx,
				Conf:    s.conf,
			})
		}()
	}

	recreate()

	recreating := false
	recreateTimer := newEmptyTimer()

	for {
		select {
		case err := <-runErr:
			runCtxCancel()
			logrus.Error("[source] run ", err)
			recreating = true
			recreateTimer = time.NewTimer(staticSourceHandlerRetryPause)

		case req := <-s.chInstanceSetReady:
			s.parent.staticSourceHandlerSetReady(s.ctx, req)

		case req := <-s.chInstanceSetNotReady:
			s.parent.staticSourceHandlerSetNotReady(s.ctx, req)

		case <-recreateTimer.C:
			recreate()
			recreating = false

		case <-s.ctx.Done():
			if !recreating {
				runCtxCancel()
				<-runErr
			}
			return
		}
	}
}

// setReady is called by a staticSource.
func (s *staticSourceHandler) SetReady(req defs.PathSourceStaticSetReadyReq) defs.PathSourceStaticSetReadyRes {
	req.Res = make(chan defs.PathSourceStaticSetReadyRes)
	select {
	case s.chInstanceSetReady <- req:
		res := <-req.Res

		if res.Err == nil {
			logrus.Info("[source] ready ", defs.MediasInfo(req.Desc.Medias))
		}

		return res

	case <-s.ctx.Done():
		return defs.PathSourceStaticSetReadyRes{Err: fmt.Errorf("terminated")}
	}
}

// setNotReady is called by a staticSource.
func (s *staticSourceHandler) SetNotReady(req defs.PathSourceStaticSetNotReadyReq) {
	req.Res = make(chan struct{})
	select {
	case s.chInstanceSetNotReady <- req:
		<-req.Res
	case <-s.ctx.Done():
	}
}
