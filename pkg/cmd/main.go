package main

import (
	"fmt"
	"os"
	"time"

	"bytes"

	"io/ioutil"

	log "github.com/Sirupsen/logrus"
	"github.com/matt-deboer/etcdcd/pkg/discovery"
	_ "github.com/matt-deboer/etcdcd/pkg/platform/all"
	"github.com/urfave/cli"
)

// Name is set at compile time based on the git repository
var Name string

// Version is set at compile time with the git version
var Version string

func main() {

	app := cli.NewApp()
	app.Name = Name
	app.Usage = `
		Dynamically discover etcd cluster membership for a specific platform.
		Useed to produce appropriate environment variable values for
		'ETCD_NAME', 'ETCD_INITIAL_CLUSTER', 'ETCD_INITIAL_CLUSTER_STATE', 
		and 'ETCD_PROXY' for use use in static cluster configuration mode.
		`
	app.Version = Version
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "platform",
			Usage:  "The platform ('aws' or 'vsphere')",
			EnvVar: "ETCDCD_PLATFORM",
		},
		cli.StringFlag{
			Name: "platform-config-file",
			Usage: `The platform config file; for platform 'aws', this is 
				unnecessary/ignored when using an instance role; for platform
				'vsphere', this file uses the same format as required by the
				vsphere kubernetes cloud provider`,
			EnvVar: "ETCDCD_PLATFORM_CONFIG",
		},
		cli.StringFlag{
			Name: "output-file",
			Usage: `The path to the output file where results will be 
				written; uses STDOUT if not specified`,
			EnvVar: "ETCDCD_OUTPUT_FILE",
		},
		cli.IntFlag{
			Name:   "client-port",
			Value:  2379,
			Usage:  "The port advertised for etcd client access",
			EnvVar: "ETCDCD_CLIENT_PORT",
		},
		cli.IntFlag{
			Name:   "server-port",
			Value:  2380,
			Usage:  "The port advertised for etcd client access",
			EnvVar: "ETCDCD_SERVER_PORT",
		},
		cli.StringFlag{
			Name:   "client-scheme",
			Value:  "http",
			Usage:  "The scheme for etcd client access urls",
			EnvVar: "ETCDCD_CLIENT_SCHEME",
		},
		cli.StringFlag{
			Name:   "server-scheme",
			Value:  "http",
			Usage:  "The scheme for etcd server access urls",
			EnvVar: "ETCDCD_SERVER_SCHEME",
		},
		cli.BoolFlag{
			Name:   "proxy",
			Usage:  "Whether to enable proxy mode",
			EnvVar: "ETCDCD_PROXY_MODE",
		},
		cli.StringFlag{
			Name: "master-names",
			Usage: `The naming pattern used to locate the masters; for platform 'aws', 
				this will be the name of the masters autoscaling group; 
				for platform 'vsphere', this will be a name-glob matching the vm names 
				of the masters (e.g., 'k8s-master-*')`,
			EnvVar: "ETCDCD_MASTER_NAMES",
		},
		cli.BoolFlag{
			Name:   "dry-run",
			Usage:  "Don't perform any changes; instead log what would have been done",
			EnvVar: "ETCDCD_DRY_RUN",
		},
		cli.BoolFlag{
			Name:   "verbose, V",
			Usage:  "Log extra information about steps taken",
			EnvVar: "ETCDCD_VERBOSE",
		},
		cli.BoolFlag{
			Name: "ignore-naming-mismatch",
			Usage: `Whether to ignore names (and only compare peer urls) when 
				looking for existing members`,
			EnvVar: "ETCDCD_IGNORE_NAMING_MISMATCH",
		},
		cli.StringFlag{
			Name:  "minimum-uptime-to-join",
			Value: "5m",
			Usage: `The minimum amount of seconds after which it is viable to join an existing cluster in which
				the current node is an expected member`,
		},
	}
	app.Action = func(c *cli.Context) {

		if c.Bool("verbose") {
			log.SetLevel(log.DebugLevel)
		}
		environment, err := parseArgs(c).DiscoverEnvironment()
		if err != nil {
			log.Fatalf("Environment discovery failed; %v", err)
		}

		log.Infof("Environment discovery success: results: %v", environment)
		out := bytes.NewBufferString("")
		for k, v := range environment {
			fmt.Fprintf(out, "%s=\"%s\"\n", k, v)
		}
		if outputFile := c.String("output-file"); len(outputFile) > 0 {
			ioutil.WriteFile(outputFile, out.Bytes(), 0644)
		} else {
			fmt.Println(out.String())
		}
	}
	app.Run(os.Args)

}

func parseArgs(c *cli.Context) *discovery.Discovery {

	platform := c.String("platform")
	if len(platform) == 0 {
		log.Fatalf("'%s' is required", "platform")
	}
	masterFilter := c.String("master-names")
	if len(masterFilter) == 0 {
		log.Fatalf("'%s' is required", "master-names")
	}
	minUptimeString := c.String("minimum-uptime-to-join")
	minJoinUptime, err := time.ParseDuration(c.String("minimum-uptime-to-join"))
	if err != nil {
		log.Fatalf("Invalid duration '%s': %v", minUptimeString, err)
	}

	return &discovery.Discovery{
		Platform:             platform,
		ConfigFile:           c.String("platform-config-file"),
		ClientPort:           c.Int("client-port"),
		ServerPort:           c.Int("server-port"),
		ClientScheme:         c.String("client-scheme"),
		ServerScheme:         c.String("server-scheme"),
		ProxyMode:            c.Bool("proxy"),
		IgnoreNamingMismatch: c.Bool("ignore-naming-mismatch"),
		MasterFilter:         masterFilter,
		MaxTries:             5,
		MinimumUptimeToJoin:  minJoinUptime,
	}

}
