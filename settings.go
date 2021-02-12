package main

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

type empty int

const (
	devNull = empty(0)

	defaultRebalanceTimer = 15 * time.Second
	defaultRequestTimeout = 15 * time.Second
	defaultConnectTimeout = 30 * time.Second

	defaultKeepaliveTime    = 10 * time.Second
	defaultKeepaliveTimeout = 10 * time.Second

	cfgListenAddress = "listen_address"

	// KeepAlive
	cfgKeepaliveTime                = "keepalive.time"
	cfgKeepaliveTimeout             = "keepalive.timeout"
	cfgKeepalivePermitWithoutStream = "keepalive.permit_without_stream"

	// Web
	cfgWebReadBufferSize     = "web.read_buffer_size"
	cfgWebWriteBufferSize    = "web.write_buffer_size"
	cfgWebReadTimeout        = "web.read_timeout"
	cfgWebWriteTimeout       = "web.write_timeout"
	cfgWebStreamRequestBody  = "web.stream_request_body"
	cfgWebMaxRequestBodySize = "web.max_request_body_size"

	// Timeouts
	cfgConTimeout = "connect_timeout"
	cfgReqTimeout = "request_timeout"
	cfgRebalance  = "rebalance_timer"

	// Logger:
	cfgLoggerLevel              = "logger.level"
	cfgLoggerFormat             = "logger.format"
	cfgLoggerTraceLevel         = "logger.trace_level"
	cfgLoggerNoCaller           = "logger.no_caller"
	cfgLoggerNoDisclaimer       = "logger.no_disclaimer"
	cfgLoggerSamplingInitial    = "logger.sampling.initial"
	cfgLoggerSamplingThereafter = "logger.sampling.thereafter"

	// Uploader Header
	cfgUploaderHeaderEnableDefaultTimestamp = "upload_header.use_default_timestamp"

	// Peers
	cfgPeers = "peers"

	// Application
	cfgApplicationName      = "app.name"
	cfgApplicationVersion   = "app.version"
	cfgApplicationBuildTime = "app.build_time"

	// command line args
	cmdHelp     = "help"
	cmdVersion  = "version"
	cmdVerbose  = "verbose"
	cmdPprof    = "pprof"
	cmdMetrics  = "metrics"
	cmdNeoFSKey = "key"
)

var ignore = map[string]struct{}{
	cfgApplicationName:      {},
	cfgApplicationVersion:   {},
	cfgApplicationBuildTime: {},

	cfgPeers: {},

	cmdHelp:    {},
	cmdVersion: {},
}

func (empty) Read([]byte) (int, error) { return 0, io.EOF }

// checkAndEnableStreaming is temporary shim, should be used before
// `StreamRequestBody` is not merged in fasthttp master
// TODO should be removed in future
func checkAndEnableStreaming(l *zap.Logger, v *viper.Viper, i interface{}) {
	vi := reflect.ValueOf(i)

	if vi.Type().Kind() != reflect.Ptr {
		return
	}

	field := vi.Elem().FieldByName("StreamRequestBody")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		l.Warn("stream request body not supported")

		return
	}

	field.SetBool(v.GetBool(cfgWebStreamRequestBody))
}

func settings() *viper.Viper {
	v := viper.New()
	v.AutomaticEnv()
	v.SetEnvPrefix(Prefix)
	v.SetConfigType("yaml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// flags setup:
	flags := pflag.NewFlagSet("commandline", pflag.ExitOnError)
	flags.SetOutput(os.Stdout)
	flags.SortFlags = false

	flags.Bool(cmdPprof, false, "enable pprof")
	flags.Bool(cmdMetrics, false, "enable prometheus")

	help := flags.BoolP(cmdHelp, "h", false, "show help")
	version := flags.BoolP(cmdVersion, "v", false, "show version")

	flags.String(cmdNeoFSKey, "", `"Path to private key file, hex string or wif`)

	flags.Bool(cmdVerbose, false, "debug gRPC connections")
	flags.Duration(cfgConTimeout, defaultConnectTimeout, "gRPC connect timeout")
	flags.Duration(cfgReqTimeout, defaultRequestTimeout, "gRPC request timeout")
	flags.Duration(cfgRebalance, defaultRebalanceTimer, "gRPC connection rebalance timer")

	flags.String(cfgListenAddress, "0.0.0.0:8082", "HTTP Gateway listen address")
	peers := flags.StringArrayP(cfgPeers, "p", nil, "NeoFS nodes")

	// set prefers:
	v.Set(cfgApplicationName, "neofs-http-gw")
	v.Set(cfgApplicationVersion, Version)

	// set defaults:

	// logger:
	v.SetDefault(cfgLoggerLevel, "debug")
	v.SetDefault(cfgLoggerFormat, "console")
	v.SetDefault(cfgLoggerTraceLevel, "panic")
	v.SetDefault(cfgLoggerNoCaller, false)
	v.SetDefault(cfgLoggerNoDisclaimer, true)
	v.SetDefault(cfgLoggerSamplingInitial, 1000)
	v.SetDefault(cfgLoggerSamplingThereafter, 1000)

	// keepalive:
	// If set below 10s, a minimum value of 10s will be used instead.
	v.SetDefault(cfgKeepaliveTime, defaultKeepaliveTime)
	v.SetDefault(cfgKeepaliveTimeout, defaultKeepaliveTimeout)
	v.SetDefault(cfgKeepalivePermitWithoutStream, true)

	// web-server:
	v.SetDefault(cfgWebReadBufferSize, 4096)
	v.SetDefault(cfgWebWriteBufferSize, 4096)
	v.SetDefault(cfgWebReadTimeout, time.Second*15)
	v.SetDefault(cfgWebWriteTimeout, time.Minute)
	v.SetDefault(cfgWebStreamRequestBody, true)
	v.SetDefault(cfgWebMaxRequestBodySize, fasthttp.DefaultMaxRequestBodySize)

	// upload header
	v.SetDefault(cfgUploaderHeaderEnableDefaultTimestamp, false)

	if err := v.BindPFlags(flags); err != nil {
		panic(err)
	}

	if err := v.ReadConfig(devNull); err != nil {
		panic(err)
	}

	if err := flags.Parse(os.Args); err != nil {
		panic(err)
	}

	switch {
	case help != nil && *help:
		fmt.Printf("NeoFS HTTP Gateway %s (%s)\n", Version, Build)
		flags.PrintDefaults()

		fmt.Println()
		fmt.Println("Default environments:")
		fmt.Println()
		keys := v.AllKeys()
		sort.Strings(keys)

		for i := range keys {
			if _, ok := ignore[keys[i]]; ok {
				continue
			}

			k := strings.Replace(keys[i], ".", "_", -1)
			fmt.Printf("%s_%s = %v\n", Prefix, strings.ToUpper(k), v.Get(keys[i]))
		}

		fmt.Println()
		fmt.Println("Peers preset:")
		fmt.Println()

		fmt.Printf("%s_%s_[N]_ADDRESS = string\n", Prefix, strings.ToUpper(cfgPeers))
		fmt.Printf("%s_%s_[N]_WEIGHT = 0..1 (float)\n", Prefix, strings.ToUpper(cfgPeers))

		os.Exit(0)
	case version != nil && *version:
		fmt.Printf("NeoFS HTTP Gateway %s (%s)\n", Version, Build)
		os.Exit(0)
	}

	if peers != nil && len(*peers) > 0 {
		for i := range *peers {
			v.SetDefault(cfgPeers+"."+strconv.Itoa(i)+".address", (*peers)[i])
			v.SetDefault(cfgPeers+"."+strconv.Itoa(i)+".weight", 1)
		}
	}

	return v
}
