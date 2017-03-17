package main

import (
	"fmt"
	"os"

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
	app.Usage = `etcdcd

		Dynamically discover etcd cluster membership for a specific platform
		`
	app.Version = Version
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "platform",
			Usage:  "The platform",
			EnvVar: "ETCDCD_PLATFORM",
		},
		cli.StringFlag{
			Name:   "platform-config-file, c",
			Usage:  "The platform config file",
			EnvVar: "ETCDCD_PLATFORM_CONFIG",
		},
		cli.StringFlag{
			Name:   "output-file, o",
			Usage:  "The path to the output file where results will be written; uses STDOUT if not specified",
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
			Name:   "master-name-filter",
			Usage:  "Masters' names will contain this string",
			EnvVar: "ETCDCD_MASTER_NAME_FILTER",
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
			Name:   "ignore-naming-mismatch",
			Usage:  "Whether to ignore names (and only compare peer urls) when looking for existing members",
			EnvVar: "ETCDCD_IGNORE_NAMING_MISMATCH",
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
	masterFilter := c.String("master-name-filter")
	if len(masterFilter) == 0 {
		log.Fatalf("'%s' is required", "master-name-filter")
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
	}
}
