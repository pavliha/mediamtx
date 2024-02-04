// Package core contains the main struct of the software.
package core

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/davecgh/go-spew/spew"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/servers/webrtc"
)

var version = "v0.0.0"

var defaultConfPaths = []string{
	"mediamtx.yml",
}

var cli struct {
	Version  bool   `help:"print version"`
	Confpath string `arg:"" default:""`
}

// Core is an instance of MediaMTX.
type Core struct {
	ctx          context.Context
	ctxCancel    func()
	confPath     string
	conf         *conf.Conf
	logger       *logger.Logger
	pathManager  *pathManager
	webRTCServer *webrtc.Server

	// out
	done chan struct{}
}

// New allocates a Core.
func New(args []string) (*Core, bool) {
	parser, err := kong.New(&cli,
		kong.Description("MediaMTX "+version),
		kong.UsageOnError(),
		kong.ValueFormatter(func(value *kong.Value) string {
			switch value.Name {
			case "confpath":
				return "path to a config file. The default is mediamtx.yml."

			default:
				return kong.DefaultHelpValueFormatter(value)
			}
		}))
	if err != nil {
		panic(err)
	}

	_, err = parser.Parse(args)
	parser.FatalIfErrorf(err)

	if cli.Version {
		fmt.Println(version)
		os.Exit(0)
	}

	ctx, ctxCancel := context.WithCancel(context.Background())

	p := &Core{
		ctx:       ctx,
		ctxCancel: ctxCancel,
		done:      make(chan struct{}),
	}

	p.conf, p.confPath, err = conf.Load(cli.Confpath, defaultConfPaths)
	if err != nil {
		fmt.Printf("ERR: %s\n", err)
		return nil, false
	}

	err = p.createResources()
	if err != nil {
		if p.logger != nil {
			p.Log(logger.Error, "%s", err)
		} else {
			fmt.Printf("ERR: %s\n", err)
		}
		p.closeResources(nil, false)
		return nil, false
	}

	go p.run()

	return p, true
}

// Close closes Core and waits for all goroutines to return.
func (p *Core) Close() {
	p.ctxCancel()
	<-p.done
}

// Wait waits for the Core to exit.
func (p *Core) Wait() {
	<-p.done
}

// Log implements logger.Writer.
func (p *Core) Log(level logger.Level, format string, args ...interface{}) {
	p.logger.Log(level, format, args...)
}

func (p *Core) run() {
	defer close(p.done)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

outer:
	for {
		select {
		case <-interrupt:
			p.Log(logger.Info, "shutting down gracefully")
			break outer

		case <-p.ctx.Done():
			break outer
		}
	}

	p.ctxCancel()

	p.closeResources(nil, false)
}

func (p *Core) createResources() error {
	var err error

	if p.logger == nil {
		p.logger, err = logger.New(
			logger.Level(p.conf.LogLevel),
			p.conf.LogDestinations,
			p.conf.LogFile,
		)
		if err != nil {
			return err
		}
	}

	if p.confPath != "" {
		a, _ := filepath.Abs(p.confPath)
		p.Log(logger.Info, "configuration loaded from %s", a)
	} else {
		list := make([]string, len(defaultConfPaths))
		for i, pa := range defaultConfPaths {
			a, _ := filepath.Abs(pa)
			list[i] = a
		}

		p.Log(logger.Warn,
			"configuration file not found (looked in %s), using an empty configuration",
			strings.Join(list, ", "))
	}

	p.pathManager = &pathManager{
		logLevel:     p.conf.LogLevel,
		readTimeout:  p.conf.ReadTimeout,
		writeTimeout: p.conf.WriteTimeout,
		pathConfs:    p.conf.Paths,
		parent:       p,
	}
	p.pathManager.initialize()

	spew.Dump(p.conf)

	i := &webrtc.Server{
		Address:               p.conf.WebRTCAddress,
		Encryption:            p.conf.WebRTCEncryption,
		AllowOrigin:           p.conf.WebRTCAllowOrigin,
		ReadTimeout:           p.conf.ReadTimeout,
		LocalUDPAddress:       p.conf.WebRTCLocalUDPAddress,
		LocalTCPAddress:       p.conf.WebRTCLocalTCPAddress,
		IPsFromInterfaces:     p.conf.WebRTCIPsFromInterfaces,
		IPsFromInterfacesList: p.conf.WebRTCIPsFromInterfacesList,
		AdditionalHosts:       p.conf.WebRTCAdditionalHosts,
		ICEServers:            p.conf.WebRTCICEServers2,
		PathManager:           p.pathManager,
		Parent:                p,
	}
	err = i.Initialize()
	if err != nil {
		return err
	}
	p.webRTCServer = i

	return nil
}

func (p *Core) closeResources(newConf *conf.Conf, calledByAPI bool) {
	closeLogger := newConf == nil ||
		newConf.LogLevel != p.conf.LogLevel ||
		!reflect.DeepEqual(newConf.LogDestinations, p.conf.LogDestinations) ||
		newConf.LogFile != p.conf.LogFile

	closePathManager := newConf == nil ||
		newConf.LogLevel != p.conf.LogLevel ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		closeLogger

	closeWebRTCServer := newConf == nil ||
		newConf.WebRTC != p.conf.WebRTC ||
		newConf.WebRTCAddress != p.conf.WebRTCAddress ||
		newConf.WebRTCEncryption != p.conf.WebRTCEncryption ||
		newConf.WebRTCAllowOrigin != p.conf.WebRTCAllowOrigin ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WebRTCLocalUDPAddress != p.conf.WebRTCLocalUDPAddress ||
		newConf.WebRTCLocalTCPAddress != p.conf.WebRTCLocalTCPAddress ||
		newConf.WebRTCIPsFromInterfaces != p.conf.WebRTCIPsFromInterfaces ||
		!reflect.DeepEqual(newConf.WebRTCIPsFromInterfacesList, p.conf.WebRTCIPsFromInterfacesList) ||
		!reflect.DeepEqual(newConf.WebRTCAdditionalHosts, p.conf.WebRTCAdditionalHosts) ||
		!reflect.DeepEqual(newConf.WebRTCICEServers2, p.conf.WebRTCICEServers2) ||
		closePathManager ||
		closeLogger

	if closeWebRTCServer && p.webRTCServer != nil {

		p.webRTCServer.Close()
		p.webRTCServer = nil
	}

	if closePathManager && p.pathManager != nil {

		p.pathManager.close()
		p.pathManager = nil
	}

	if newConf == nil {
		p.Log(logger.Info, "waiting for running hooks")
	}

	if closeLogger && p.logger != nil {
		p.logger.Close()
		p.logger = nil
	}
}
