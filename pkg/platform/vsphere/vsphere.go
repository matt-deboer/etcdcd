package vsphere

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"gopkg.in/gcfg.v1"

	log "github.com/Sirupsen/logrus"
	"github.com/matt-deboer/etcdcd/pkg/platform"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"

	etcd "github.com/coreos/etcd/client"
)

const (
	ActivePowerState         = "poweredOn"
	RoundTripperDefaultCount = 3
	MaxWaitForVMAddresses    = (time.Minute * 10)
)

var clientLock sync.Mutex

// VSphere is an implementation of cloud provider Interface for VSphere.
type VSphere struct {
	client *govmomi.Client
	cfg    *VSphereConfig
	// InstanceID of the server where this VSphere object is instantiated.
	localInstanceID string
}

type VSphereConfig struct {
	Global struct {
		// vCenter username.
		User string `gcfg:"user"`
		// vCenter password in clear text.
		Password string `gcfg:"password"`
		// vCenter IP.
		VCenterIP string `gcfg:"server"`
		// vCenter port.
		VCenterPort string `gcfg:"port"`
		// True if vCenter uses self-signed cert.
		InsecureFlag bool `gcfg:"insecure-flag"`
		// Datacenter in which VMs are located.
		Datacenter string `gcfg:"datacenter"`
		// Datastore in which vmdks are stored.
		Datastore string `gcfg:"datastore"`
		// WorkingDir is path where VMs can be found.
		WorkingDir string `gcfg:"working-dir"`
		// Soap round tripper count (retries = RoundTripper - 1)
		RoundTripperCount uint `gcfg:"soap-roundtrip-count"`
		// VMUUID is the VM Instance UUID of virtual machine which can be retrieved from instanceUuid
		// property in VmConfigInfo, or also set as vc.uuid in VMX file.
		// If not set, will be fetched from the machine via sysfs (requires root)
		VMUUID string `gcfg:"vm-uuid"`
	}
	Network struct {
		// PublicNetwork is name of the network the VMs are joined to.
		PublicNetwork string `gcfg:"public-network"`
	}
	Disk struct {
		// SCSIControllerType defines SCSI controller to be used.
		SCSIControllerType string `dcfg:"scsicontrollertype"`
	}
}

func init() {
	platform.Register("vsphere", func(config io.Reader) (platform.Platform, error) {
		cfg, err := readConfig(config)
		if err != nil {
			return nil, err
		}
		return newVSphere(cfg)
	})
}

// Parses vSphere cloud config file and stores it into VSphereConfig.
func readConfig(config io.Reader) (VSphereConfig, error) {
	if config == nil {
		err := fmt.Errorf("no config file given")
		return VSphereConfig{}, err
	}

	var cfg VSphereConfig
	err := gcfg.ReadInto(&cfg, config)
	return cfg, err
}

// ExpectedMembers returns a list of members that should form the cluster
func (vs *VSphere) ExpectedMembers(
	memberFilter string, clientScheme string, clientPort int, serverScheme string, serverPort int) ([]etcd.Member, error) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := vSphereLogin(ctx, vs)
	if err != nil {
		log.Errorf("VCenter login failed; %v", err)
		return nil, err
	}

	members := []etcd.Member{}
	names, err := vs.list(memberFilter)
	if err != nil {
		return nil, err
	}

	timeout := time.Now().Add(MaxWaitForVMAddresses)
	now := time.Now()
	for len(members) < len(names) && now.Before(timeout) {
		members = []etcd.Member{}
		for _, name := range names {
			member := etcd.Member{Name: name, ClientURLs: []string{}, PeerURLs: []string{}}
			addrs, err := vs.getAddresses(name)
			if err != nil {
				return nil, err
			}
			for _, a := range addrs {
				addr := a
				if strings.Contains(a, ":") {
					addr = "[" + a + "]"
				}
				member.ClientURLs = append(member.ClientURLs, fmt.Sprintf("%s://%s:%d", clientScheme, addr, clientPort))
				member.PeerURLs = append(member.PeerURLs, fmt.Sprintf("%s://%s:%d", serverScheme, addr, serverPort))
			}
			if len(member.ClientURLs) > 0 {
				members = append(members, member)
			}
		}
		if len(members) < len(names) {
			sleepTime := (5 * time.Second)
			if log.GetLevel() >= log.DebugLevel {
				log.Debugf("Still waiting to resolve addresses for %d out of %d members; sleeping for %s",
					len(members), (len(names) - len(members)), sleepTime)
			}
			time.Sleep(sleepTime)
			now = time.Now().Add(time.Second)
			if now.After(timeout) {
				return nil, fmt.Errorf("Timed out waiting for %v to resolve addresses", names)
			}
		}
	}
	return members, nil
}

// LocalInstanceName returns a list of members that should form the cluster
func (vs *VSphere) LocalInstanceName() string {
	return vs.localInstanceID
}

// Returns the name of the VM on which this code is running.
// Prerequisite: this code assumes VMWare vmtools or open-vm-tools to be installed in the VM.
// Will attempt to determine the machine's name via it's UUID in this precedence order, failing if neither have a UUID:
// * cloud config value VMUUID
// * sysfs entry
func getVMName(client *govmomi.Client, cfg *VSphereConfig) (string, error) {

	var vmUUID string

	if cfg.Global.VMUUID != "" {
		vmUUID = cfg.Global.VMUUID
	} else {
		// This needs root privileges on the host, and will fail otherwise.
		vmUUIDbytes, err := ioutil.ReadFile("/sys/devices/virtual/dmi/id/product_uuid")
		if err != nil {
			return "", err
		}

		vmUUID = string(vmUUIDbytes)
		cfg.Global.VMUUID = vmUUID
	}

	if vmUUID == "" {
		return "", fmt.Errorf("unable to determine machine ID from cloud configuration or sysfs")
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a new finder
	f := find.NewFinder(client.Client, true)

	// Fetch and set data center
	dc, err := f.Datacenter(ctx, cfg.Global.Datacenter)
	if err != nil {
		return "", err
	}
	f.SetDatacenter(dc)

	s := object.NewSearchIndex(client.Client)

	svm, err := s.FindByUuid(ctx, dc, strings.ToLower(strings.TrimSpace(vmUUID)), true, nil)
	if err != nil {
		return "", err
	}

	var vm mo.VirtualMachine
	err = s.Properties(ctx, svm.Reference(), []string{"name"}, &vm)
	if err != nil {
		return "", err
	}
	if log.GetLevel() >= log.DebugLevel {
		log.Debugf("getVMName: vm.Name=%s", vm.Name)
	}
	return vm.Name, nil
}

func newVSphere(cfg VSphereConfig) (*VSphere, error) {
	clientLock.Lock()
	defer clientLock.Unlock()

	if cfg.Global.WorkingDir != "" {
		cfg.Global.WorkingDir = path.Clean(cfg.Global.WorkingDir) + "/"
	}
	if cfg.Global.RoundTripperCount == 0 {
		cfg.Global.RoundTripperCount = RoundTripperDefaultCount
	}
	if cfg.Global.VCenterPort != "" {
		log.Warningf("port is a deprecated field in vsphere.conf and will be removed in future release.")
	}

	c, err := newClient(context.TODO(), &cfg)
	if err != nil {
		return nil, err
	}

	id, err := getVMName(c, &cfg)
	if err != nil {
		return nil, err
	}

	vs := VSphere{
		client:          c,
		cfg:             &cfg,
		localInstanceID: id,
	}
	runtime.SetFinalizer(&vs, logout)

	if log.GetLevel() >= log.DebugLevel {
		log.Debugf("newVSphere: vs: %#v", vs)
	}

	return &vs, nil
}

func logout(vs *VSphere) {
	vs.client.Logout(context.TODO())
}

func newClient(ctx context.Context, cfg *VSphereConfig) (*govmomi.Client, error) {
	// Parse URL from string
	u, err := url.Parse(fmt.Sprintf("https://%s/sdk", cfg.Global.VCenterIP))
	if err != nil {
		return nil, err
	}
	// set username and password for the URL
	u.User = url.UserPassword(cfg.Global.User, cfg.Global.Password)

	// Connect and log in to ESX or vCenter
	c, err := govmomi.NewClient(ctx, u, cfg.Global.InsecureFlag)
	if err != nil {
		return nil, err
	}

	// Add retry functionality
	c.RoundTripper = vim25.Retry(c.RoundTripper, vim25.TemporaryNetworkError(int(cfg.Global.RoundTripperCount)))

	return c, nil
}

// Returns a client which communicates with vCenter.
// This client can used to perform further vCenter operations.
func vSphereLogin(ctx context.Context, vs *VSphere) error {
	var err error
	clientLock.Lock()
	defer clientLock.Unlock()
	if vs.client == nil {
		vs.client, err = newClient(ctx, vs.cfg)
		if err != nil {
			return err
		}
		return nil
	}

	m := session.NewManager(vs.client.Client)
	// retrieve client's current session
	u, err := m.UserSession(ctx)
	if err != nil {
		log.Errorf("Error while obtaining user session. err: %q", err)
		return err
	}
	if u != nil {
		return nil
	}

	log.Warningf("Creating new client session since the existing session is not valid or not authenticated")
	vs.client.Logout(ctx)
	vs.client, err = newClient(ctx, vs.cfg)
	if err != nil {
		return err
	}

	return nil
}

// Returns vSphere object `virtual machine` by its name.
func getVirtualMachineByName(ctx context.Context, cfg *VSphereConfig, c *govmomi.Client, nodeName string) (*object.VirtualMachine, error) {
	name := nodeNameToVMName(nodeName)

	if log.GetLevel() >= log.DebugLevel {
		log.Debugf("getVirtualMachineByName: name=%s", name)
	}

	// Create a new finder
	f := find.NewFinder(c.Client, true)

	// Fetch and set data center
	dc, err := f.Datacenter(ctx, cfg.Global.Datacenter)
	if err != nil {
		return nil, err
	}
	f.SetDatacenter(dc)

	vmRegex := cfg.Global.WorkingDir + name

	// Retrieve vm by name
	//TODO: also look for vm inside subfolders
	vm, err := f.VirtualMachine(ctx, vmRegex)
	if err != nil {
		return nil, err
	}

	return vm, nil
}

func getVirtualMachineManagedObjectReference(ctx context.Context, c *govmomi.Client, vm *object.VirtualMachine, field string, dst interface{}) error {
	collector := property.DefaultCollector(c.Client)

	// Retrieve required field from VM object
	err := collector.RetrieveOne(ctx, vm.Reference(), []string{field}, dst)
	if err != nil {
		return err
	}
	return nil
}

// Returns names of running VMs inside VM folder.
func getInstances(ctx context.Context, cfg *VSphereConfig, c *govmomi.Client, filter string) ([]string, error) {
	if log.GetLevel() >= log.DebugLevel {
		log.Debugf("getInstances: filter=%s", filter)
	}

	f := find.NewFinder(c.Client, true)
	dc, err := f.Datacenter(ctx, cfg.Global.Datacenter)
	if err != nil {
		return nil, err
	}

	f.SetDatacenter(dc)

	vmRegex := cfg.Global.WorkingDir + filter

	//TODO: get all vms inside subfolders
	vms, err := f.VirtualMachineList(ctx, vmRegex)
	if err != nil {
		return nil, err
	}

	var vmRef []types.ManagedObjectReference
	for _, vm := range vms {
		vmRef = append(vmRef, vm.Reference())
	}

	pc := property.DefaultCollector(c.Client)

	var vmt []mo.VirtualMachine
	err = pc.Retrieve(ctx, vmRef, []string{"name", "summary"}, &vmt)
	if err != nil {
		return nil, err
	}

	var vmList []string
	for _, vm := range vmt {
		if vm.Summary.Runtime.PowerState == ActivePowerState {
			vmList = append(vmList, vm.Name)
		} else if vm.Summary.Config.Template == false {
			log.Warningf("VM %s, is not in %s state", vm.Name, ActivePowerState)
		}
	}
	return vmList, nil
}

// List returns names of VMs (inside vm folder) by applying filter and which are currently running.
func (vs *VSphere) list(filter string) ([]string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vmList, err := getInstances(ctx, vs.cfg, vs.client, filter)
	if err != nil {
		return nil, err
	}

	if log.GetLevel() >= log.DebugLevel {
		log.Debugf("Found %d instances matching %s:[ %s ]",
			len(vmList), filter, strings.Join(vmList, ", "))
	}

	var nodeNames []string
	for _, n := range vmList {
		nodeNames = append(nodeNames, n)
	}
	return nodeNames, nil
}

func (vs *VSphere) getAddresses(nodeName string) ([]string, error) {
	addrs := []string{}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vm, err := getVirtualMachineByName(ctx, vs.cfg, vs.client, nodeName)
	if err != nil {
		return nil, err
	}

	var mvm mo.VirtualMachine
	err = getVirtualMachineManagedObjectReference(ctx, vs.client, vm, "guest.net", &mvm)
	if err != nil {
		return nil, err
	}

	// retrieve VM's ip(s)
	for _, v := range mvm.Guest.Net {
		if v.Network == vs.cfg.Network.PublicNetwork {
			for _, ip := range v.IpAddress {
				if len(ip) > 0 {
					addrs = append(addrs, ip)
				}
			}
		} else if log.GetLevel() >= log.DebugLevel {
			log.Debugf("getAddresses: nodeName=%s, net %v are not in configured network", nodeName, v.IpAddress)
		}
	}
	return addrs, nil
}

// nodeNameToVMName maps a NodeName to the vmware infrastructure name
func nodeNameToVMName(nodeName string) string {
	return string(nodeName)
}
