// Package conf contains the struct that holds the configuration of the software.
package conf

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/headers"

	"github.com/bluenviron/mediamtx/internal/conf/yaml"
	"github.com/bluenviron/mediamtx/internal/logger"
)

// ErrPathNotFound is returned when a path is not found.
var ErrPathNotFound = errors.New("path not found")

func sortedKeys(paths map[string]*OptionalPath) []string {
	ret := make([]string, len(paths))
	i := 0
	for name := range paths {
		ret[i] = name
		i++
	}
	sort.Strings(ret)
	return ret
}

func firstThatExists(paths []string) string {
	for _, pa := range paths {
		_, err := os.Stat(pa)
		if err == nil {
			return pa
		}
	}
	return ""
}

func contains(list []headers.AuthMethod, item headers.AuthMethod) bool {
	for _, i := range list {
		if i == item {
			return true
		}
	}
	return false
}

func copyStructFields(dest interface{}, source interface{}) {
	rvsource := reflect.ValueOf(source).Elem()
	rvdest := reflect.ValueOf(dest)
	nf := rvsource.NumField()
	var zero reflect.Value

	for i := 0; i < nf; i++ {
		fnew := rvsource.Field(i)
		f := rvdest.Elem().FieldByName(rvsource.Type().Field(i).Name)
		if f == zero {
			continue
		}

		if fnew.Kind() == reflect.Pointer {
			if !fnew.IsNil() {
				if f.Kind() == reflect.Ptr {
					f.Set(fnew)
				} else {
					f.Set(fnew.Elem())
				}
			}
		} else {
			f.Set(fnew)
		}
	}
}

// Conf is a configuration.
type Conf struct {
	// General
	LogLevel          LogLevel        `json:"logLevel"`
	LogDestinations   LogDestinations `json:"logDestinations"`
	LogFile           string          `json:"logFile"`
	ReadTimeout       StringDuration  `json:"readTimeout"`
	WriteTimeout      StringDuration  `json:"writeTimeout"`
	ReadBufferCount   *int            `json:"readBufferCount,omitempty"` // deprecated
	WriteQueueSize    int             `json:"writeQueueSize"`
	UDPMaxPayloadSize int             `json:"udpMaxPayloadSize"`

	// RTSP server
	RTSP              bool      `json:"rtsp"`
	RTSPDisable       *bool     `json:"rtspDisable,omitempty"` // deprecated
	Protocols         Protocols `json:"protocols"`
	RTSPAddress       string    `json:"rtspAddress"`
	RTSPSAddress      string    `json:"rtspsAddress"`
	RTPAddress        string    `json:"rtpAddress"`
	RTCPAddress       string    `json:"rtcpAddress"`
	MulticastIPRange  string    `json:"multicastIPRange"`
	MulticastRTPPort  int       `json:"multicastRTPPort"`
	MulticastRTCPPort int       `json:"multicastRTCPPort"`
	ServerKey         string    `json:"serverKey"`
	ServerCert        string    `json:"serverCert"`

	// WebRTC server
	WebRTC                      bool              `json:"webrtc"`
	WebRTCDisable               *bool             `json:"webrtcDisable,omitempty"` // deprecated
	WebRTCAddress               string            `json:"webrtcAddress"`
	WebRTCEncryption            bool              `json:"webrtcEncryption"`
	WebRTCServerKey             string            `json:"webrtcServerKey"`
	WebRTCServerCert            string            `json:"webrtcServerCert"`
	WebRTCAllowOrigin           string            `json:"webrtcAllowOrigin"`
	WebRTCTrustedProxies        IPsOrCIDRs        `json:"webrtcTrustedProxies"`
	WebRTCLocalUDPAddress       string            `json:"webrtcLocalUDPAddress"`
	WebRTCLocalTCPAddress       string            `json:"webrtcLocalTCPAddress"`
	WebRTCIPsFromInterfaces     bool              `json:"webrtcIPsFromInterfaces"`
	WebRTCIPsFromInterfacesList []string          `json:"webrtcIPsFromInterfacesList"`
	WebRTCAdditionalHosts       []string          `json:"webrtcAdditionalHosts"`
	WebRTCICEServers2           []WebRTCICEServer `json:"webrtcICEServers2"`
	WebRTCICEUDPMuxAddress      *string           `json:"webrtcICEUDPMuxAddress,omitempty"`  // deprecated
	WebRTCICETCPMuxAddress      *string           `json:"webrtcICETCPMuxAddress,omitempty"`  // deprecated
	WebRTCICEHostNAT1To1IPs     *[]string         `json:"webrtcICEHostNAT1To1IPs,omitempty"` // deprecated
	WebRTCICEServers            *[]string         `json:"webrtcICEServers,omitempty"`        // deprecated

	// Path defaults
	PathDefaults Path `json:"pathDefaults"`

	// Paths
	OptionalPaths map[string]*OptionalPath `json:"paths"`
	Paths         map[string]*Path         `json:"-"` // filled by Check()
}

func (conf *Conf) setDefaults() {
	// General
	conf.LogLevel = LogLevel(logger.Info)
	conf.LogDestinations = LogDestinations{logger.DestinationStdout}
	conf.LogFile = "mediamtx.log"
	conf.ReadTimeout = 10 * StringDuration(time.Second)
	conf.WriteTimeout = 10 * StringDuration(time.Second)
	conf.WriteQueueSize = 512
	conf.UDPMaxPayloadSize = 1472

	// RTSP server
	conf.RTSP = true
	conf.Protocols = Protocols{
		Protocol(gortsplib.TransportUDP):          {},
		Protocol(gortsplib.TransportUDPMulticast): {},
		Protocol(gortsplib.TransportTCP):          {},
	}
	conf.RTSPAddress = ":8554"
	conf.RTSPSAddress = ":8322"
	conf.RTPAddress = ":8000"
	conf.RTCPAddress = ":8001"
	conf.MulticastIPRange = "224.1.0.0/16"
	conf.MulticastRTPPort = 8002
	conf.MulticastRTCPPort = 8003
	conf.ServerKey = "server.key"
	conf.ServerCert = "server.crt"

	// WebRTC server
	conf.WebRTC = true
	conf.WebRTCAddress = ":8889"
	conf.WebRTCServerKey = "server.key"
	conf.WebRTCServerCert = "server.crt"
	conf.WebRTCAllowOrigin = "*"
	conf.WebRTCLocalUDPAddress = ":8189"
	conf.WebRTCIPsFromInterfaces = true
	conf.WebRTCIPsFromInterfacesList = []string{}
	conf.WebRTCAdditionalHosts = []string{}
	conf.WebRTCICEServers2 = []WebRTCICEServer{}

	conf.PathDefaults.setDefaults()
}

// Load loads a Conf.
func Load(fpath string, defaultConfPaths []string) (*Conf, string, error) {
	conf := &Conf{}

	fpath, err := conf.loadFromFile(fpath, defaultConfPaths)
	if err != nil {
		return nil, "", err
	}

	err = conf.Validate()
	if err != nil {
		return nil, "", err
	}

	return conf, fpath, nil
}

func (conf *Conf) loadFromFile(fpath string, defaultConfPaths []string) (string, error) {
	if fpath == "" {
		fpath = firstThatExists(defaultConfPaths)

		// when the configuration file is not explicitly set,
		// it is optional.
		if fpath == "" {
			conf.setDefaults()
			return "", nil
		}
	}

	byts, err := os.ReadFile(fpath)
	if err != nil {
		return "", err
	}

	err = yaml.Load(byts, conf)
	if err != nil {
		return "", err
	}

	return fpath, nil
}

// Clone clones the configuration.
func (conf Conf) Clone() *Conf {
	enc, err := json.Marshal(conf)
	if err != nil {
		panic(err)
	}

	var dest Conf
	err = json.Unmarshal(enc, &dest)
	if err != nil {
		panic(err)
	}

	return &dest
}

// Validate checks the configuration for errors.
func (conf *Conf) Validate() error {
	// General

	if conf.ReadBufferCount != nil {
		conf.WriteQueueSize = *conf.ReadBufferCount
	}
	if (conf.WriteQueueSize & (conf.WriteQueueSize - 1)) != 0 {
		return fmt.Errorf("'writeQueueSize' must be a power of two")
	}
	if conf.UDPMaxPayloadSize > 1472 {
		return fmt.Errorf("'udpMaxPayloadSize' must be less than 1472")
	}

	// RTSP

	if conf.RTSPDisable != nil {
		conf.RTSP = !*conf.RTSPDisable
	}

	// WebRTC

	if conf.WebRTCDisable != nil {
		conf.WebRTC = !*conf.WebRTCDisable
	}
	if conf.WebRTCICEUDPMuxAddress != nil {
		conf.WebRTCLocalUDPAddress = *conf.WebRTCICEUDPMuxAddress
	}
	if conf.WebRTCICETCPMuxAddress != nil {
		conf.WebRTCLocalTCPAddress = *conf.WebRTCICETCPMuxAddress
	}
	if conf.WebRTCICEHostNAT1To1IPs != nil {
		conf.WebRTCAdditionalHosts = *conf.WebRTCICEHostNAT1To1IPs
	}
	if conf.WebRTCICEServers != nil {
		for _, server := range *conf.WebRTCICEServers {
			parts := strings.Split(server, ":")
			if len(parts) == 5 {
				conf.WebRTCICEServers2 = append(conf.WebRTCICEServers2, WebRTCICEServer{
					URL:      parts[0] + ":" + parts[3] + ":" + parts[4],
					Username: parts[1],
					Password: parts[2],
				})
			} else {
				conf.WebRTCICEServers2 = append(conf.WebRTCICEServers2, WebRTCICEServer{
					URL: server,
				})
			}
		}
	}
	for _, server := range conf.WebRTCICEServers2 {
		if !strings.HasPrefix(server.URL, "stun:") &&
			!strings.HasPrefix(server.URL, "turn:") &&
			!strings.HasPrefix(server.URL, "turns:") {
			return fmt.Errorf("invalid ICE server: '%s'", server.URL)
		}
	}
	if conf.WebRTCLocalUDPAddress == "" &&
		conf.WebRTCLocalTCPAddress == "" &&
		len(conf.WebRTCICEServers2) == 0 {
		return fmt.Errorf("at least one between 'webrtcLocalUDPAddress'," +
			" 'webrtcLocalTCPAddress' or 'webrtcICEServers2' must be filled")
	}
	if conf.WebRTCLocalUDPAddress != "" || conf.WebRTCLocalTCPAddress != "" {
		if !conf.WebRTCIPsFromInterfaces && len(conf.WebRTCAdditionalHosts) == 0 {
			return fmt.Errorf("at least one between 'webrtcIPsFromInterfaces' or 'webrtcAdditionalHosts' must be filled")
		}
	}

	hasAllOthers := false
	for name := range conf.OptionalPaths {
		if name == "all" || name == "all_others" || name == "~^.*$" {
			if hasAllOthers {
				return fmt.Errorf("all_others, all and '~^.*$' are aliases")
			}
			hasAllOthers = true
		}
	}

	conf.Paths = make(map[string]*Path)

	for _, name := range sortedKeys(conf.OptionalPaths) {
		optional := conf.OptionalPaths[name]
		if optional == nil {
			optional = &OptionalPath{
				Values: newOptionalPathValues(),
			}
		}

		pconf := newPath(&conf.PathDefaults, optional)
		conf.Paths[name] = pconf

		err := pconf.validate(conf, name)
		if err != nil {
			return err
		}
	}

	return nil
}
