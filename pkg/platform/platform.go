package platform

import (
	"io"
	"os"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/coreos/etcd/client"
)

// Platform is an abstract pluggable interface for etcd initial state discovery
type Platform interface {

	// ExpectedMembers returns a list of members that should form the cluster
	ExpectedMembers(nameFilter string) ([]client.Member, error)

	// LocalInstanceName returns the instance name for the local instance
	LocalInstanceName() string
}

// Factory produces a platform instance using the provided configuration
type Factory func(config io.Reader) (Platform, error)

// All registered platforms
var (
	platformsMutex sync.Mutex
	platforms      = make(map[string]Factory)
)

// Register registers a cloudprovider.Factory by name.  This
// is expected to happen during app startup.
func Register(name string, factory Factory) {
	platformsMutex.Lock()
	defer platformsMutex.Unlock()
	if _, found := platforms[name]; found {
		log.Fatalf("Platform %q was registered more than once", name)
	}
	log.Infof("Registered platform %q", name)
	platforms[name] = factory
}

// Get creates an instance of the named platform, or nil if
// the name is unknown.  The error return is only used if the named provider
// was known but failed to initialize. The config parameter specifies the
// path of the configuration file for the platform, or empty for no configuation.
func Get(name string, configPath string) (Platform, error) {
	platformsMutex.Lock()
	defer platformsMutex.Unlock()
	f, found := platforms[name]
	if !found {
		return nil, nil
	}
	if configPath != "" {
		var config *os.File
		config, err := os.Open(configPath)
		if err != nil {
			log.Fatalf("Couldn't open cloud provider configuration %s: %#v",
				configPath, err)
		}

		defer config.Close()
		return f(config)
	}
	return f(nil)

}
