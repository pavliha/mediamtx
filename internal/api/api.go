// Package api contains the API server.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/httpserv"
	"github.com/bluenviron/mediamtx/internal/restrictnetwork"
	"github.com/bluenviron/mediamtx/internal/servers/rtsp"
	"github.com/bluenviron/mediamtx/internal/servers/webrtc"
)

func interfaceIsEmpty(i interface{}) bool {
	return reflect.ValueOf(i).Kind() != reflect.Ptr || reflect.ValueOf(i).IsNil()
}

func paginate2(itemsPtr interface{}, itemsPerPage int, page int) int {
	ritems := reflect.ValueOf(itemsPtr).Elem()

	itemsLen := ritems.Len()
	if itemsLen == 0 {
		return 0
	}

	pageCount := (itemsLen / itemsPerPage)
	if (itemsLen % itemsPerPage) != 0 {
		pageCount++
	}

	min := page * itemsPerPage
	if min > itemsLen {
		min = itemsLen
	}

	max := (page + 1) * itemsPerPage
	if max > itemsLen {
		max = itemsLen
	}

	ritems.Set(ritems.Slice(min, max))

	return pageCount
}

func paginate(itemsPtr interface{}, itemsPerPageStr string, pageStr string) (int, error) {
	itemsPerPage := 100

	if itemsPerPageStr != "" {
		tmp, err := strconv.ParseUint(itemsPerPageStr, 10, 31)
		if err != nil {
			return 0, err
		}
		itemsPerPage = int(tmp)
	}

	page := 0

	if pageStr != "" {
		tmp, err := strconv.ParseUint(pageStr, 10, 31)
		if err != nil {
			return 0, err
		}
		page = int(tmp)
	}

	return paginate2(itemsPtr, itemsPerPage, page), nil
}

func sortedKeys(paths map[string]*conf.Path) []string {
	ret := make([]string, len(paths))
	i := 0
	for name := range paths {
		ret[i] = name
		i++
	}
	sort.Strings(ret)
	return ret
}

func paramName(ctx *gin.Context) (string, bool) {
	name := ctx.Param("name")

	if len(name) < 2 || name[0] != '/' {
		return "", false
	}

	return name[1:], true
}

// PathManager contains methods used by the API and Metrics server.
type PathManager interface {
	APIPathsList() (*defs.APIPathList, error)
	APIPathsGet(string) (*defs.APIPath, error)
}

// HLSServer contains methods used by the API and Metrics server.
type HLSServer interface {
	APIMuxersList() (*defs.APIHLSMuxerList, error)
	APIMuxersGet(string) (*defs.APIHLSMuxer, error)
}

// RTSPServer contains methods used by the API and Metrics server.
type RTSPServer interface {
	APIConnsList() (*defs.APIRTSPConnsList, error)
	APIConnsGet(uuid.UUID) (*defs.APIRTSPConn, error)
	APISessionsList() (*defs.APIRTSPSessionList, error)
	APISessionsGet(uuid.UUID) (*defs.APIRTSPSession, error)
	APISessionsKick(uuid.UUID) error
}

// RTMPServer contains methods used by the API and Metrics server.
type RTMPServer interface {
	APIConnsList() (*defs.APIRTMPConnList, error)
	APIConnsGet(uuid.UUID) (*defs.APIRTMPConn, error)
	APIConnsKick(uuid.UUID) error
}

// SRTServer contains methods used by the API and Metrics server.
type SRTServer interface {
	APIConnsList() (*defs.APISRTConnList, error)
	APIConnsGet(uuid.UUID) (*defs.APISRTConn, error)
	APIConnsKick(uuid.UUID) error
}

// WebRTCServer contains methods used by the API and Metrics server.
type WebRTCServer interface {
	APISessionsList() (*defs.APIWebRTCSessionList, error)
	APISessionsGet(uuid.UUID) (*defs.APIWebRTCSession, error)
	APISessionsKick(uuid.UUID) error
}

type apiParent interface {
	logger.Writer
	APIConfigSet(conf *conf.Conf)
}

// API is an API server.
type API struct {
	Address      string
	ReadTimeout  conf.StringDuration
	Conf         *conf.Conf
	PathManager  PathManager
	RTSPServer   RTSPServer
	RTSPSServer  RTSPServer
	WebRTCServer WebRTCServer
	Parent       apiParent

	httpServer *httpserv.WrappedServer
	mutex      sync.RWMutex
}

// Initialize initializes API.
func (a *API) Initialize() error {
	router := gin.New()
	router.SetTrustedProxies(nil) //nolint:errcheck

	group := router.Group("/")

	group.GET("/v3/config/global/get", a.onConfigGlobalGet)
	group.PATCH("/v3/config/global/patch", a.onConfigGlobalPatch)

	group.GET("/v3/config/pathdefaults/get", a.onConfigPathDefaultsGet)
	group.PATCH("/v3/config/pathdefaults/patch", a.onConfigPathDefaultsPatch)

	group.GET("/v3/config/paths/list", a.onConfigPathsList)
	group.GET("/v3/config/paths/get/*name", a.onConfigPathsGet)
	group.POST("/v3/config/paths/add/*name", a.onConfigPathsAdd)
	group.PATCH("/v3/config/paths/patch/*name", a.onConfigPathsPatch)
	group.POST("/v3/config/paths/replace/*name", a.onConfigPathsReplace)
	group.DELETE("/v3/config/paths/delete/*name", a.onConfigPathsDelete)

	group.GET("/v3/paths/list", a.onPathsList)
	group.GET("/v3/paths/get/*name", a.onPathsGet)

	if !interfaceIsEmpty(a.RTSPServer) {
		group.GET("/v3/rtspconns/list", a.onRTSPConnsList)
		group.GET("/v3/rtspconns/get/:id", a.onRTSPConnsGet)
		group.GET("/v3/rtspsessions/list", a.onRTSPSessionsList)
		group.GET("/v3/rtspsessions/get/:id", a.onRTSPSessionsGet)
		group.POST("/v3/rtspsessions/kick/:id", a.onRTSPSessionsKick)
	}

	if !interfaceIsEmpty(a.RTSPSServer) {
		group.GET("/v3/rtspsconns/list", a.onRTSPSConnsList)
		group.GET("/v3/rtspsconns/get/:id", a.onRTSPSConnsGet)
		group.GET("/v3/rtspssessions/list", a.onRTSPSSessionsList)
		group.GET("/v3/rtspssessions/get/:id", a.onRTSPSSessionsGet)
		group.POST("/v3/rtspssessions/kick/:id", a.onRTSPSSessionsKick)
	}

	if !interfaceIsEmpty(a.WebRTCServer) {
		group.GET("/v3/webrtcsessions/list", a.onWebRTCSessionsList)
		group.GET("/v3/webrtcsessions/get/:id", a.onWebRTCSessionsGet)
		group.POST("/v3/webrtcsessions/kick/:id", a.onWebRTCSessionsKick)
	}

	network, address := restrictnetwork.Restrict("tcp", a.Address)

	var err error
	a.httpServer, err = httpserv.NewWrappedServer(
		network,
		address,
		time.Duration(a.ReadTimeout),
		"",
		"",
		router,
		a,
	)
	if err != nil {
		return err
	}

	a.Log(logger.Info, "listener opened on "+address)

	return nil
}

// Close closes the API.
func (a *API) Close() {
	a.Log(logger.Info, "listener is closing")
	a.httpServer.Close()
}

// Log implements logger.Writer.
func (a *API) Log(level logger.Level, format string, args ...interface{}) {
	a.Parent.Log(level, "[API] "+format, args...)
}

func (a *API) writeError(ctx *gin.Context, status int, err error) {
	// show error in logs
	a.Log(logger.Error, err.Error())

	// add error to response
	ctx.JSON(status, &defs.APIError{
		Error: err.Error(),
	})
}

func (a *API) onConfigGlobalGet(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	ctx.JSON(http.StatusOK, c.Global())
}

func (a *API) onConfigGlobalPatch(ctx *gin.Context) {
	var c conf.OptionalGlobal
	err := json.NewDecoder(ctx.Request.Body).Decode(&c)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()

	newConf.PatchGlobal(&c)

	err = newConf.Validate()
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf

	// since reloading the configuration can cause the shutdown of the API,
	// call it in a goroutine
	go a.Parent.APIConfigSet(newConf)

	ctx.Status(http.StatusOK)
}

func (a *API) onConfigPathDefaultsGet(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	ctx.JSON(http.StatusOK, c.PathDefaults)
}

func (a *API) onConfigPathDefaultsPatch(ctx *gin.Context) {
	var p conf.OptionalPath
	err := json.NewDecoder(ctx.Request.Body).Decode(&p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()

	newConf.PatchPathDefaults(&p)

	err = newConf.Validate()
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	ctx.Status(http.StatusOK)
}

func (a *API) onConfigPathsList(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	data := &defs.APIPathConfList{
		Items: make([]*conf.Path, len(c.Paths)),
	}

	for i, key := range sortedKeys(c.Paths) {
		data.Items[i] = c.Paths[key]
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onConfigPathsGet(ctx *gin.Context) {
	name, ok := paramName(ctx)
	if !ok {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	p, ok := c.Paths[name]
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("path configuration not found"))
		return
	}

	ctx.JSON(http.StatusOK, p)
}

func (a *API) onConfigPathsAdd(ctx *gin.Context) { //nolint:dupl
	name, ok := paramName(ctx)
	if !ok {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}

	var p conf.OptionalPath
	err := json.NewDecoder(ctx.Request.Body).Decode(&p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()

	err = newConf.AddPath(name, &p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	err = newConf.Validate()
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	ctx.Status(http.StatusOK)
}

func (a *API) onConfigPathsPatch(ctx *gin.Context) { //nolint:dupl
	name, ok := paramName(ctx)
	if !ok {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}

	var p conf.OptionalPath
	err := json.NewDecoder(ctx.Request.Body).Decode(&p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()

	err = newConf.PatchPath(name, &p)
	if err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}

	err = newConf.Validate()
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	ctx.Status(http.StatusOK)
}

func (a *API) onConfigPathsReplace(ctx *gin.Context) { //nolint:dupl
	name, ok := paramName(ctx)
	if !ok {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}

	var p conf.OptionalPath
	err := json.NewDecoder(ctx.Request.Body).Decode(&p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()

	err = newConf.ReplacePath(name, &p)
	if err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}

	err = newConf.Validate()
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	ctx.Status(http.StatusOK)
}

func (a *API) onConfigPathsDelete(ctx *gin.Context) {
	name, ok := paramName(ctx)
	if !ok {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()

	err := newConf.RemovePath(name)
	if err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}

	err = newConf.Validate()
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	ctx.Status(http.StatusOK)
}

func (a *API) onPathsList(ctx *gin.Context) {
	data, err := a.PathManager.APIPathsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onPathsGet(ctx *gin.Context) {
	name, ok := paramName(ctx)
	if !ok {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}

	data, err := a.PathManager.APIPathsGet(name)
	if err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPConnsList(ctx *gin.Context) {
	data, err := a.RTSPServer.APIConnsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPConnsGet(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	data, err := a.RTSPServer.APIConnsGet(uuid)
	if err != nil {
		if errors.Is(err, rtsp.ErrConnNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPSessionsList(ctx *gin.Context) {
	data, err := a.RTSPServer.APISessionsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPSessionsGet(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	data, err := a.RTSPServer.APISessionsGet(uuid)
	if err != nil {
		if errors.Is(err, rtsp.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPSessionsKick(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	err = a.RTSPServer.APISessionsKick(uuid)
	if err != nil {
		if errors.Is(err, rtsp.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.Status(http.StatusOK)
}

func (a *API) onRTSPSConnsList(ctx *gin.Context) {
	data, err := a.RTSPSServer.APIConnsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPSConnsGet(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	data, err := a.RTSPSServer.APIConnsGet(uuid)
	if err != nil {
		if errors.Is(err, rtsp.ErrConnNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPSSessionsList(ctx *gin.Context) {
	data, err := a.RTSPSServer.APISessionsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPSSessionsGet(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	data, err := a.RTSPSServer.APISessionsGet(uuid)
	if err != nil {
		if errors.Is(err, rtsp.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onRTSPSSessionsKick(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	err = a.RTSPSServer.APISessionsKick(uuid)
	if err != nil {
		if errors.Is(err, rtsp.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.Status(http.StatusOK)
}

func (a *API) onWebRTCSessionsList(ctx *gin.Context) {
	data, err := a.WebRTCServer.APISessionsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onWebRTCSessionsGet(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	data, err := a.WebRTCServer.APISessionsGet(uuid)
	if err != nil {
		if errors.Is(err, webrtc.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onWebRTCSessionsKick(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	err = a.WebRTCServer.APISessionsKick(uuid)
	if err != nil {
		if errors.Is(err, webrtc.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.Status(http.StatusOK)
}

// ReloadConf is called by core.
func (a *API) ReloadConf(conf *conf.Conf) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.Conf = conf
}
