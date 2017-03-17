package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	etcd "github.com/coreos/etcd/client"
	"github.com/matt-deboer/etcdcd/pkg/platform"
)

// Discovery provides correct startup details for etcd with respect to
// known vs. expected cluster membership
type Discovery struct {
	ConfigFile   string
	Platform     string
	ClientPort   int
	ServerPort   int
	ClientScheme string
	ServerScheme string
	MaxTries     int
	ProxyMode    bool
	MasterFilter string
}

func findMemberByName(members []etcd.Member, name string) *etcd.Member {
	for _, member := range members {
		if name == member.Name {
			return &member
		}
	}
	return nil
}

func containsMember(members []etcd.Member, member etcd.Member) bool {
	for _, master := range members {
		for _, peerURL := range master.PeerURLs {
			for _, memberPeerURL := range member.PeerURLs {
				if peerURL == memberPeerURL {
					return true
				}
			}
		}
	}
	return false
}

// DiscoverEnvironment produces an environment hash
func (d *Discovery) DiscoverEnvironment() (map[string]string, error) {

	p, err := platform.Get(d.Platform, d.ConfigFile)
	if p == nil {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("No such platform: " + d.Platform)
	}

	var expectedMembers []etcd.Member
	if members, err := p.ExpectedMembers(d.MasterFilter); err == nil {
		for _, m := range members {
			// have to cast here because of golang type-system--ugh!
			expectedMembers = append(expectedMembers, etcd.Member(m))
		}
	} else {
		return nil, err
	}

	var etcdMembers []etcd.Member
	ctx := context.Background()

	var membersAPI etcd.MembersAPI
	resolved := false
	for resolved {
		for _, master := range expectedMembers {

			cfg := etcd.Config{
				Endpoints: master.ClientURLs,
				Transport: etcd.DefaultTransport,
				// set timeout per request to fail fast when the target endpoint is unavailable
				HeaderTimeoutPerRequest: time.Second,
			}
			etcdClient, err := etcd.New(cfg)
			if err != nil {
				log.Warnf("Error connecting to %s [ %s ], %v", master.Name)
				continue
			}

			membersAPI := etcd.NewMembersAPI(etcdClient)
			etcdMembers, err = membersAPI.List(ctx)
			if err != nil {
				log.Warnf("Error listing members %s [ %s ], %v", master.Name)
				continue
			}
			break
		}
		if !resolved {
			// TODO: what's our timeout here?
			time.Sleep(10 * time.Second)
		}
	}

	environment := map[string]string{}
	environment["ETCD_NAME"] = p.LocalInstanceName()
	environment["ETCD_INITIAL_CLUSTER"] = initialClusterString(expectedMembers)

	localMaster := findMemberByName(etcdMembers, p.LocalInstanceName())
	if len(etcdMembers) > 0 && localMaster != nil {
		d.evictBadPeers(membersAPI, expectedMembers, etcdMembers)

		if d.ProxyMode {
			environment["ETCD_PROXY"] = "on"
		} else {
			log.Infof("Joining existing cluster as a master")
			if err := d.joinExistingCluster(membersAPI, *localMaster); err != nil {
				log.Fatal(err)
			}
		}
		environment["ETCD_INITIAL_CLUSTER_STATE"] = "existing"
	} else {
		log.Infof("Creating a new cluster")
		environment["ETCD_INITIAL_CLUSTER_STATE"] = "new"
	}
	return environment, nil
}

func initialClusterString(members []etcd.Member) string {
	initialCluster := make([]string, len(members), 0)
	for _, m := range members {
		member := fmt.Sprintf("%s=%s", m.Name, m.PeerURLs[0])
		initialCluster = append(initialCluster, member)
	}
	return strings.Join(initialCluster, ",")
}

func (d *Discovery) evictBadPeers(membersAPI etcd.MembersAPI, expectedMembers []etcd.Member, etcdMembers []etcd.Member) {
	for _, peer := range etcdMembers {
		if !containsMember(expectedMembers, peer) {
			msg := fmt.Sprintf("Ejecting bad peer %s %v from the cluster:", peer.Name, peer.PeerURLs)
			for tries := 0; tries < d.MaxTries; tries++ {
				err := membersAPI.Remove(context.Background(), peer.ID)
				if err == nil {
					log.Infof("%s DONE", msg)
					break
				} else if (tries + 1) == d.MaxTries {
					log.Errorf("%s ERROR: %v", msg, err)
				}
			}
		}
	}
}

func (d *Discovery) joinExistingCluster(membersAPI etcd.MembersAPI, localMember etcd.Member) error {
	msg := "Joining existing cluster: "
	for tries := 0; tries < d.MaxTries; tries++ {
		_, err := membersAPI.Add(context.Background(), localMember.PeerURLs[0])
		if err == nil {
			log.Infof("%s DONE", msg)
			break
		} else if (tries + 1) == d.MaxTries {
			log.Errorf("%s ERROR: %v", msg, err)
			return err
		}
	}
	return nil
}
