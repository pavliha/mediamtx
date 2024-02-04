package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
)

type pathManagerHLSServer interface {
	PathReady(defs.Path)
	PathNotReady(defs.Path)
}

type pathManagerParent interface {
	logger.Writer
}

type pathManager struct {
	logLevel          conf.LogLevel
	readTimeout       conf.StringDuration
	writeTimeout      conf.StringDuration
	writeQueueSize    int
	udpMaxPayloadSize int
	pathConfs         map[string]*conf.Path
	parent            pathManagerParent

	ctx         context.Context
	ctxCancel   func()
	wg          sync.WaitGroup
	hlsManager  pathManagerHLSServer
	paths       map[string]*path
	pathsByConf map[string]map[*path]struct{}

	// in
	chSetHLSServer chan pathManagerHLSServer
	chClosePath    chan *path
	chPathReady    chan *path
	chPathNotReady chan *path
	chFindPathConf chan defs.PathFindPathConfReq
	chDescribe     chan defs.PathDescribeReq
	chAddReader    chan defs.PathAddReaderReq
	chAddPublisher chan defs.PathAddPublisherReq
}

func (pm *pathManager) initialize() {
	ctx, ctxCancel := context.WithCancel(context.Background())

	pm.ctx = ctx
	pm.ctxCancel = ctxCancel
	pm.paths = make(map[string]*path)
	pm.pathsByConf = make(map[string]map[*path]struct{})
	pm.chSetHLSServer = make(chan pathManagerHLSServer)
	pm.chClosePath = make(chan *path)
	pm.chPathReady = make(chan *path)
	pm.chPathNotReady = make(chan *path)
	pm.chFindPathConf = make(chan defs.PathFindPathConfReq)
	pm.chDescribe = make(chan defs.PathDescribeReq)
	pm.chAddReader = make(chan defs.PathAddReaderReq)
	pm.chAddPublisher = make(chan defs.PathAddPublisherReq)

	for pathConfName, pathConf := range pm.pathConfs {
		if pathConf.Regexp == nil {
			pm.createPath(pathConfName, pathConf, pathConfName, nil)
		}
	}

	pm.Log(logger.Debug, "path manager created")

	pm.wg.Add(1)
	go pm.run()
}

func (pm *pathManager) close() {
	pm.Log(logger.Debug, "path manager is shutting down")
	pm.ctxCancel()
	pm.wg.Wait()
}

// Log implements logger.Writer.
func (pm *pathManager) Log(level logger.Level, format string, args ...interface{}) {
	pm.parent.Log(level, format, args...)
}

func (pm *pathManager) run() {
	defer pm.wg.Done()

outer:
	for {
		select {

		case pa := <-pm.chClosePath:
			pm.doClosePath(pa)

		case pa := <-pm.chPathReady:
			pm.doPathReady(pa)

		case pa := <-pm.chPathNotReady:
			pm.doPathNotReady(pa)

		case req := <-pm.chFindPathConf:
			pm.doFindPathConf(req)

		case req := <-pm.chDescribe:
			pm.doDescribe(req)

		case req := <-pm.chAddReader:
			pm.doAddReader(req)

		case req := <-pm.chAddPublisher:
			pm.doAddPublisher(req)

		case <-pm.ctx.Done():
			break outer
		}
	}

	pm.ctxCancel()
}

func (pm *pathManager) doClosePath(pa *path) {
	if pmpa, ok := pm.paths[pa.name]; !ok || pmpa != pa {
		return
	}
	pm.removePath(pa)
}

func (pm *pathManager) doPathReady(pa *path) {
	if pm.hlsManager != nil {
		pm.hlsManager.PathReady(pa)
	}
}

func (pm *pathManager) doPathNotReady(pa *path) {
	if pm.hlsManager != nil {
		pm.hlsManager.PathNotReady(pa)
	}
}

func (pm *pathManager) doFindPathConf(req defs.PathFindPathConfReq) {
	_, pathConf, _, err := conf.FindPathConf(pm.pathConfs, req.AccessRequest.Name)
	if err != nil {
		req.Res <- defs.PathFindPathConfRes{Err: err}
		return
	}

	req.Res <- defs.PathFindPathConfRes{Conf: pathConf}
}

func (pm *pathManager) doDescribe(req defs.PathDescribeReq) {
	pathConfName, pathConf, pathMatches, err := conf.FindPathConf(pm.pathConfs, req.AccessRequest.Name)
	if err != nil {
		req.Res <- defs.PathDescribeRes{Err: err}
		return
	}

	// create path if it doesn't exist
	if _, ok := pm.paths[req.AccessRequest.Name]; !ok {
		pm.createPath(pathConfName, pathConf, req.AccessRequest.Name, pathMatches)
	}

	req.Res <- defs.PathDescribeRes{Path: pm.paths[req.AccessRequest.Name]}
}

func (pm *pathManager) doAddReader(req defs.PathAddReaderReq) {
	pathConfName, pathConf, pathMatches, err := conf.FindPathConf(pm.pathConfs, req.AccessRequest.Name)
	if err != nil {
		req.Res <- defs.PathAddReaderRes{Err: err}
		return
	}

	// create path if it doesn't exist
	if _, ok := pm.paths[req.AccessRequest.Name]; !ok {
		pm.createPath(pathConfName, pathConf, req.AccessRequest.Name, pathMatches)
	}

	req.Res <- defs.PathAddReaderRes{Path: pm.paths[req.AccessRequest.Name]}
}

func (pm *pathManager) doAddPublisher(req defs.PathAddPublisherReq) {

	req.Res <- defs.PathAddPublisherRes{Path: pm.paths[req.AccessRequest.Name]}
}

func (pm *pathManager) createPath(
	pathConfName string,
	pathConf *conf.Path,
	name string,
	matches []string,
) {
	pa := &path{
		parentCtx:         pm.ctx,
		logLevel:          pm.logLevel,
		readTimeout:       pm.readTimeout,
		writeTimeout:      pm.writeTimeout,
		writeQueueSize:    pm.writeQueueSize,
		udpMaxPayloadSize: pm.udpMaxPayloadSize,
		confName:          pathConfName,
		conf:              pathConf,
		name:              name,
		matches:           matches,
		wg:                &pm.wg,
		parent:            pm,
	}
	pa.initialize()

	pm.paths[name] = pa

	if _, ok := pm.pathsByConf[pathConfName]; !ok {
		pm.pathsByConf[pathConfName] = make(map[*path]struct{})
	}
	pm.pathsByConf[pathConfName][pa] = struct{}{}
}

func (pm *pathManager) removePath(pa *path) {
	delete(pm.pathsByConf[pa.confName], pa)
	if len(pm.pathsByConf[pa.confName]) == 0 {
		delete(pm.pathsByConf, pa.confName)
	}
	delete(pm.paths, pa.name)
}

// pathReady is called by path.
func (pm *pathManager) pathReady(pa *path) {
	select {
	case pm.chPathReady <- pa:
	case <-pm.ctx.Done():
	case <-pa.ctx.Done(): // in case pathManager is blocked by path.wait()
	}
}

// pathNotReady is called by path.
func (pm *pathManager) pathNotReady(pa *path) {
	select {
	case pm.chPathNotReady <- pa:
	case <-pm.ctx.Done():
	case <-pa.ctx.Done(): // in case pathManager is blocked by path.wait()
	}
}

// closePath is called by path.
func (pm *pathManager) closePath(pa *path) {
	select {
	case pm.chClosePath <- pa:
	case <-pm.ctx.Done():
	case <-pa.ctx.Done(): // in case pathManager is blocked by path.wait()
	}
}

// GetConfForPath is called by a reader or publisher.
func (pm *pathManager) FindPathConf(req defs.PathFindPathConfReq) defs.PathFindPathConfRes {
	req.Res = make(chan defs.PathFindPathConfRes)
	select {
	case pm.chFindPathConf <- req:
		return <-req.Res

	case <-pm.ctx.Done():
		return defs.PathFindPathConfRes{Err: fmt.Errorf("terminated")}
	}
}

// Describe is called by a reader or publisher.
func (pm *pathManager) Describe(req defs.PathDescribeReq) defs.PathDescribeRes {
	req.Res = make(chan defs.PathDescribeRes)
	select {
	case pm.chDescribe <- req:
		res1 := <-req.Res
		if res1.Err != nil {
			return res1
		}

		res2 := res1.Path.(*path).describe(req)
		if res2.Err != nil {
			return res2
		}

		res2.Path = res1.Path
		return res2

	case <-pm.ctx.Done():
		return defs.PathDescribeRes{Err: fmt.Errorf("terminated")}
	}
}

// AddPublisher is called by a publisher.
func (pm *pathManager) AddPublisher(req defs.PathAddPublisherReq) defs.PathAddPublisherRes {
	req.Res = make(chan defs.PathAddPublisherRes)
	select {
	case pm.chAddPublisher <- req:
		res := <-req.Res
		if res.Err != nil {
			return res
		}

		return res.Path.(*path).addPublisher(req)

	case <-pm.ctx.Done():
		return defs.PathAddPublisherRes{Err: fmt.Errorf("terminated")}
	}
}

// AddReader is called by a reader.
func (pm *pathManager) AddReader(req defs.PathAddReaderReq) defs.PathAddReaderRes {
	req.Res = make(chan defs.PathAddReaderRes)
	select {
	case pm.chAddReader <- req:
		res := <-req.Res
		if res.Err != nil {
			return res
		}

		return res.Path.(*path).addReader(req)

	case <-pm.ctx.Done():
		return defs.PathAddReaderRes{Err: fmt.Errorf("terminated")}
	}
}

// setHLSServer is called by hlsManager.
func (pm *pathManager) setHLSServer(s pathManagerHLSServer) {
	select {
	case pm.chSetHLSServer <- s:
	case <-pm.ctx.Done():
	}
}
