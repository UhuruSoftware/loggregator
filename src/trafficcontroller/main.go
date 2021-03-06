package main

import (
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"strconv"
	"time"
	"trafficcontroller/authorization"
	"trafficcontroller/outputproxy"

	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/loggregatorlib/cfcomponent"
	"github.com/cloudfoundry/loggregatorlib/cfcomponent/registrars/collectorregistrar"
	"github.com/cloudfoundry/loggregatorlib/cfcomponent/registrars/routerregistrar"
	"github.com/cloudfoundry/loggregatorlib/clientpool"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/cloudfoundry/storeadapter/workerpool"
)

var DefaultStoreAdapterProvider = func(urls []string, concurrentRequests int) storeadapter.StoreAdapter {
	workerPool := workerpool.NewWorkerPool(concurrentRequests)

	return etcdstoreadapter.NewETCDStoreAdapter(urls, workerPool)
}

var EtcdQueryInterval = 5 * time.Second

type Config struct {
	EtcdUrls                  []string
	EtcdMaxConcurrentRequests int

	JobName  string
	JobIndex int
	Zone     string
	cfcomponent.Config
	ApiHost                 string
	Host                    string
	Loggregators            map[string][]string
	LoggregatorIncomingPort uint32
	LoggregatorOutgoingPort uint32
	IncomingPort            uint32
	OutgoingPort            uint32
	SystemDomain            string
	SkipCertVerify          bool
}

func (c *Config) setDefaults() {
	if c.LoggregatorIncomingPort == 0 {
		c.LoggregatorIncomingPort = c.IncomingPort
	}

	if c.LoggregatorOutgoingPort == 0 {
		c.LoggregatorOutgoingPort = c.OutgoingPort
	}

	if c.JobName == "" {
		c.JobName = "loggregator_trafficcontroller"
	}

	if c.EtcdMaxConcurrentRequests == 0 {
		c.EtcdMaxConcurrentRequests = 10
	}
}

func (c *Config) validate(logger *gosteno.Logger) (err error) {
	if c.SystemDomain == "" {
		return errors.New("Need system domain to register with NATS")
	}

	err = c.Validate(logger)
	return
}

var (
	logFilePath = flag.String("logFile", "", "The agent log file, defaults to STDOUT")
	logLevel    = flag.Bool("debug", false, "Debug logging")
	configFile  = flag.String("config", "config/loggregator_trafficcontroller.json", "Location of the loggregator trafficcontroller config json file")
	cpuprofile  = flag.String("cpuprofile", "", "write cpu profile to file")
	memprofile  = flag.String("memprofile", "", "write memory profile to this file")
)

func main() {
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			panic(err)
		}
		pprof.StartCPUProfile(f)
		defer func() {
			pprof.StopCPUProfile()
			f.Close()
		}()
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			panic(err)
		}
		go func() {
			defer f.Close()
			ticker := time.NewTicker(time.Second * 1)
			defer ticker.Stop()
			for {
				<-ticker.C
				pprof.WriteHeapProfile(f)
			}
		}()
	}

	config, logger, err := ParseConfig(logLevel, configFile, logFilePath)
	if err != nil {
		panic(err)
	}

	proxy := makeOutgoingProxy(config, logger)
	startOutgoingProxy(net.JoinHostPort(proxy.IpAddress, strconv.FormatUint(uint64(config.OutgoingPort), 10)), proxy)

	setupMonitoring(proxy, config, logger)

	rr := routerregistrar.NewRouterRegistrar(config.MbusClient, logger)
	uri := "loggregator." + config.SystemDomain
	err = rr.RegisterWithRouter(proxy.IpAddress, config.OutgoingPort, []string{uri})
	if err != nil {
		logger.Fatalf("Startup: Did not get response from router when greeting. Using default keep-alive for now. Err: %v.", err)
	}

	killChan := make(chan os.Signal)
	signal.Notify(killChan, os.Kill, os.Interrupt)

	for {
		select {
		case <-cfcomponent.RegisterGoRoutineDumpSignalChannel():
			cfcomponent.DumpGoRoutine()
		case <-killChan:
			rr.UnregisterFromRouter(proxy.IpAddress, config.OutgoingPort, []string{uri})
			break
		}
	}
}

func ParseConfig(logLevel *bool, configFile, logFilePath *string) (*Config, *gosteno.Logger, error) {
	config := &Config{OutgoingPort: 8080}
	err := cfcomponent.ReadConfigInto(config, *configFile)
	if err != nil {
		return nil, nil, err
	}

	config.setDefaults()
	config.Host = net.JoinHostPort(config.Host, strconv.FormatUint(uint64(config.IncomingPort), 10))
	logger := cfcomponent.NewLogger(*logLevel, *logFilePath, "loggregator trafficcontroller", config.Config)
	logger.Info("Startup: Setting up the loggregator traffic controller")

	err = config.validate(logger)
	if err != nil {
		return nil, nil, err
	}
	return config, logger, nil
}

func MakeProvider(config *Config, logger *gosteno.Logger, stopChan <-chan struct{}) outputproxy.LoggregatorServerProvider {
	clientPool := clientpool.NewLoggregatorClientPool(logger, int(config.LoggregatorOutgoingPort), false)
	adapter := DefaultStoreAdapterProvider(config.EtcdUrls, config.EtcdMaxConcurrentRequests)
	adapter.Connect()
	go clientPool.RunUpdateLoop(adapter, "/healthstatus/loggregator", stopChan, EtcdQueryInterval)
	return outputproxy.NewDynamicLoggregatorServerProvider(clientPool)
}

func makeOutgoingProxy(config *Config, logger *gosteno.Logger) *outputproxy.Proxy {
	authorizer := authorization.NewLogAccessAuthorizer(config.ApiHost, config.SkipCertVerify)
	proxy := outputproxy.NewProxy(MakeProvider(config, logger, nil), authorizer, config.Config, logger)
	return proxy
}

func setupMonitoring(proxy *outputproxy.Proxy, config *Config, logger *gosteno.Logger) {
	cr := collectorregistrar.NewCollectorRegistrar(config.MbusClient, logger)
	err := cr.RegisterWithCollector(proxy.Component)
	if err != nil {
		panic(err)
	}

	go func() {
		err := proxy.StartMonitoringEndpoints()
		if err != nil {
			panic(err)
		}
	}()
}

func startOutgoingProxy(host string, proxy http.Handler) {
	go func() {
		err := http.ListenAndServe(host, proxy)
		if err != nil {
			panic(err)
		}
	}()
}
