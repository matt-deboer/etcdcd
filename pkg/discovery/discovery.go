package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"sort"
	"strings"
	"time"

	"net/http"

	log "github.com/Sirupsen/logrus"
	etcd "github.com/coreos/etcd/client"
	"github.com/matt-deboer/etcdcd/pkg/platform"
)

// Discovery provides correct startup details for etcd with respect to
// known vs. expected cluster membership
type Discovery struct {
	ConfigFile           string
	Platform             string
	ClientPort           int
	ServerPort           int
	ClientScheme         string
	ServerScheme         string
	MaxTries             int
	ProxyMode            bool
	MasterFilter         string
	DryRun               bool
	IgnoreNamingMismatch bool
	MinimumUptimeToJoin  time.Duration
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
	for _, m := range members {
		for _, peerURL := range m.PeerURLs {
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
	for tries := 0; tries < d.MaxTries && len(expectedMembers) == 0; tries++ {
		if members, err := p.ExpectedMembers(d.MasterFilter, d.ClientScheme,
			d.ClientPort, d.ServerScheme, d.ServerPort); err == nil {
			for _, m := range members {
				// have to cast here because of golang type-system--ugh!
				expectedMembers = append(expectedMembers, etcd.Member(m))
				if len(m.PeerURLs) == 0 {
					log.Fatalf("Platform %s returned an invalid member which will be ignored: %#v", d.Platform, m)
				}
			}
		} else {
			return nil, err
		}
		if len(expectedMembers) == 0 {
			sleepTime := (2 * time.Second)
			if log.GetLevel() >= log.DebugLevel {
				log.Debugf("Failed to resolve expected members; sleeping for %s", sleepTime)
			}
			time.Sleep(sleepTime)
		}
	}

	if len(expectedMembers) == 0 {
		log.Fatal("Failed to determine expected members")
	} else if log.GetLevel() >= log.DebugLevel {
		log.Debugf("Expected cluster members: %v#", expectedMembers)
	}
	sort.Slice(expectedMembers, func(i, j int) bool { return expectedMembers[i].Name < expectedMembers[j].Name })

	localMaster := findMemberByName(expectedMembers, p.LocalInstanceName())
	membersAPI, currentMembers, uptime, err := d.resolveMembersAndAPI(expectedMembers, localMaster)

	environment := map[string]string{}
	environment["ETCD_NAME"] = p.LocalInstanceName()
	environment["ETCD_INITIAL_CLUSTER"] = initialClusterString(expectedMembers)

	if localMaster != nil {
		if log.GetLevel() >= log.DebugLevel {
			log.Debugf("Local master: %#v", *localMaster)
		}
		// this instance is an expected master
		if len(currentMembers) > 0 && !containsMember(currentMembers, *localMaster) && uptime >= d.MinimumUptimeToJoin {
			// there is an existing cluster
			if err = d.assertSaneClusterState(expectedMembers, currentMembers); err != nil {
				log.Fatal(err)
			}

			d.evictBadPeers(membersAPI, expectedMembers, currentMembers)
			log.Infof("Joining existing cluster as a master")
			// TODO: what if we encounter a state where not of the expected masters are
			// members of the current cluster?
			if err := d.joinExistingCluster(membersAPI, expectedMembers, localMaster); err != nil {
				log.Fatal(err)
			}
			environment["ETCD_INITIAL_CLUSTER_STATE"] = "existing"
		} else {
			log.Infof("Creating a new cluster")
			environment["ETCD_INITIAL_CLUSTER_STATE"] = "new"
		}
	} else if d.ProxyMode {
		log.Infof("Proxying existing cluster")
		environment["ETCD_INITIAL_CLUSTER_STATE"] = "existing"
		environment["ETCD_PROXY"] = "on"
	} else {
		return nil, fmt.Errorf(
			"Invalid cluster configuration: localhost (%s) is not an expected master, and not in proxy mode",
			p.LocalInstanceName())
	}
	return environment, nil
}

func initialClusterString(members []etcd.Member) string {
	if len(members) > 0 {
		initialCluster := make([]string, 0, len(members))
		for _, m := range members {
			member := fmt.Sprintf("%s=%s", m.Name, m.PeerURLs[0])
			initialCluster = append(initialCluster, member)
		}
		return strings.Join(initialCluster, ",")
	}
	return ""
}

// Check for mismatched names between expected and current members with
// matching peer URLs; also check for lack of intersection between
// expected and current members--indicating an invalid current (or expected)
// cluster state
func (d *Discovery) assertSaneClusterState(expectedMembers []etcd.Member, currentMembers []etcd.Member) error {
	partialMatchCount := 0
	for _, current := range currentMembers {
		for _, expected := range expectedMembers {
			matchingPeerURL := ""
			for _, expectedPeerURL := range expected.PeerURLs {
				for _, currentPeerURL := range current.PeerURLs {
					if expectedPeerURL == currentPeerURL {
						matchingPeerURL = expectedPeerURL
						partialMatchCount++
						break
					}
				}
				if len(matchingPeerURL) > 0 {
					break
				}
			}
			if len(matchingPeerURL) > 0 && len(current.Name) > 0 && current.Name != expected.Name {
				if !d.IgnoreNamingMismatch {
					return fmt.Errorf("Expected peer %s with peer URL %s already exists with a different name: %s",
						expected.Name, matchingPeerURL, current.Name)
				} else if log.GetLevel() >= log.DebugLevel {
					log.Debugf("Ignoring expected peer %s with peer URL %s already exists with a different name: %s",
						expected.Name, matchingPeerURL, current.Name)
				}
			}
		}
	}
	if partialMatchCount == 0 && len(expectedMembers) > 0 && len(currentMembers) > 0 {
		expectedJSON, _ := json.Marshal(expectedMembers)
		currentJSON, _ := json.Marshal(currentMembers)
		return fmt.Errorf("Invalid cluster state: found no intersection between peer URLs of expected members %s and current members %s",
			expectedJSON, currentJSON)
	}

	return nil
}

func (d *Discovery) evictBadPeers(membersAPI etcd.MembersAPI, expectedMembers []etcd.Member, currentMembers []etcd.Member) {

	for _, peer := range currentMembers {
		if !containsMember(expectedMembers, peer) {
			msg := fmt.Sprintf("Ejecting bad peer %s %v from the cluster:", peer.Name, peer.PeerURLs)
			if d.DryRun {
				log.Infof("DRY_RUN: would have ejected peer %s %v from the cluster", peer.Name, peer.PeerURLs)
			} else {
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
}

func (d *Discovery) joinExistingCluster(membersAPI etcd.MembersAPI,
	expectedMembers []etcd.Member, localMember *etcd.Member) error {

	msg := "Joining existing cluster: "
	for tries := 0; tries < d.MaxTries; tries++ {
		if d.DryRun {
			log.Infof("DRY_RUN: would have added %s %v to the cluster", localMember.Name, localMember.PeerURLs)
		} else {
			_, err := membersAPI.Add(context.Background(), localMember.PeerURLs[0])
			if err == nil {
				log.Infof("%s DONE", msg)
				break
			} else if (tries + 1) == d.MaxTries {
				log.Errorf("%s ERROR: %v", msg, err)
				return err
			}
			if log.GetLevel() >= log.DebugLevel {
				log.Debugf("Retryable error attempting to add local master %#v: %v", localMember, err)
			}
			membersAPI, _, _, err = d.resolveMembersAndAPI(expectedMembers, localMember)
			if err != nil {
				log.Errorf("%s ERROR: %v", msg, err)
				return err
			}
		}
	}
	return nil
}

func (d *Discovery) resolveMembersAndAPI(expectedMembers []etcd.Member,
	localMember *etcd.Member) (membersAPI etcd.MembersAPI, currentMembers []etcd.Member, uptime time.Duration, err error) {

	ctx := context.Background()
	var lastErr error
	for tries := 0; tries <= d.MaxTries; tries++ {
		for _, member := range expectedMembers {
			// don't attempt self connection; afterall, this is intended as a pre-cursor
			// to the actual etcd service on the local host
			if localMember != nil && member.PeerURLs[0] != localMember.PeerURLs[0] {
				cfg := etcd.Config{
					Endpoints: member.ClientURLs,
					Transport: etcd.DefaultTransport,
					// set timeout per request to fail fast when the target endpoint is unavailable
					HeaderTimeoutPerRequest: time.Second,
				}
				etcdClient, err := etcd.New(cfg)
				if err != nil {
					if log.GetLevel() >= log.DebugLevel {
						log.Debugf("Error connecting to %s %v, %v", member.Name, member.ClientURLs, err)
					}
					lastErr = err
					continue
				}

				membersAPI = etcd.NewMembersAPI(etcdClient)
				leader, err := membersAPI.Leader(ctx)
				if err != nil {
					if log.GetLevel() >= log.DebugLevel {
						log.Debugf("Error getting leader %s %v, %v", member.Name, member.ClientURLs, err)
					}
					lastErr = err
					continue
				} else if leader == nil {
					if log.GetLevel() >= log.DebugLevel {
						log.Debugf("Error getting leader %s %v, %v", member.Name, member.ClientURLs, err)
					}
					lastErr = errors.New("Failed to resolve cluster leader")
				}

				currentMembers, err = membersAPI.List(ctx)
				if err != nil {
					if log.GetLevel() >= log.DebugLevel {
						log.Debugf("Error listing members %s %v, %v", member.Name, member.ClientURLs, err)
					}
					lastErr = err
					continue
				}

				uptime, err = getUptime(member.ClientURLs[0])
				if err != nil {
					if log.GetLevel() >= log.DebugLevel {
						log.Debugf("Error listing leader uptime %s %v, %v", member.Name, member.ClientURLs, err)
					}
					lastErr = err
					continue
				}

				// sanity-check the returned members; it may be partial in case of a yet-forming cluster
				hasInvalidMembers := false
				for _, m := range currentMembers {
					if len(m.Name) == 0 || len(m.PeerURLs) == 0 {
						if log.GetLevel() >= log.DebugLevel {
							log.Debugf("Returned actual member list contains invalid member: %#v", m)
						}
						hasInvalidMembers = true
						break
					}
				}
				if !hasInvalidMembers {
					if log.GetLevel() >= log.DebugLevel {
						log.Debugf("Actual cluster members: %#v", currentMembers)
					}
					return membersAPI, currentMembers, uptime, nil
				}
			}
		}
		if len(currentMembers) == 0 {
			// TODO: what's our timeout here?
			sleepTime := (1 * time.Second)
			if log.GetLevel() >= log.DebugLevel {
				log.Debugf("Failed to resolve members; sleeping for %s", sleepTime)
			}
			time.Sleep(sleepTime)
		} else {
			break
		}
	}
	return nil, nil, time.Millisecond, lastErr
}

func getUptime(endpoint string) (time.Duration, error) {
	resp, err := http.DefaultClient.Get(endpoint + "/v2/stats/self")
	if err != nil {
		return time.Millisecond, err
	}

	defer resp.Body.Close()
	contents, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return time.Millisecond, err
	}
	stats := make(map[string]interface{})
	err = json.Unmarshal(contents, &stats)
	if err != nil {
		return time.Millisecond, err
	}
	if leaderInfo, ok := stats["leaderInfo"]; ok {
		if uptimeString, ok := leaderInfo.(map[string]interface{})["uptime"]; ok {
			return time.ParseDuration(uptimeString.(string))
		}
	}
	return time.Millisecond, fmt.Errorf("Missing leader uptime info for endpiont %s", endpoint)
}
