// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build cluster_proxy

package integration

import (
	"sync"

	"github.com/coreos/etcd/clientv3"
	pb "github.com/coreos/etcd/etcdserver/etcdserverpb"
	"github.com/coreos/etcd/proxy/grpcproxy"
	"github.com/coreos/etcd/proxy/grpcproxy/adapter"
)

var (
	pmu     sync.Mutex
	proxies map[*clientv3.Client]grpcClientProxy = make(map[*clientv3.Client]grpcClientProxy)
)

type grpcClientProxy struct {
	grpc    grpcAPI
	wdonec  <-chan struct{}
	kvdonec <-chan struct{}
	lpdonec <-chan struct{}
}

func toGRPC(c *clientv3.Client) grpcAPI {
	pmu.Lock()
	defer pmu.Unlock()

	if v, ok := proxies[c]; ok {
		return v.grpc
	}
	kvp, kvpch := grpcproxy.NewKvProxy(c)
	wp, wpch := grpcproxy.NewWatchProxy(c)
	lp, lpch := grpcproxy.NewLeaseProxy(c)
	grpc := grpcAPI{
		pb.NewClusterClient(c.ActiveConnection()),
		adapter.KvServerToKvClient(kvp),
		adapter.LeaseServerToLeaseClient(lp),
		adapter.WatchServerToWatchClient(wp),
		pb.NewMaintenanceClient(c.ActiveConnection()),
		pb.NewAuthClient(c.ActiveConnection()),
	}
	proxies[c] = grpcClientProxy{grpc: grpc, wdonec: wpch, kvdonec: kvpch, lpdonec: lpch}
	return grpc
}

type proxyCloser struct {
	clientv3.Watcher
	wdonec  <-chan struct{}
	kvdonec <-chan struct{}
	lpdonec <-chan struct{}
}

func (pc *proxyCloser) Close() error {
	// client ctx is canceled before calling close, so kv and lp will close out
	<-pc.kvdonec
	err := pc.Watcher.Close()
	<-pc.wdonec
	<-pc.lpdonec
	return err
}

func newClientV3(cfg clientv3.Config) (*clientv3.Client, error) {
	c, err := clientv3.New(cfg)
	if err != nil {
		return nil, err
	}
	rpc := toGRPC(c)
	c.KV = clientv3.NewKVFromKVClient(rpc.KV)
	pmu.Lock()
	c.Lease = clientv3.NewLeaseFromLeaseClient(rpc.Lease, cfg.DialTimeout)
	c.Watcher = &proxyCloser{
		Watcher: clientv3.NewWatchFromWatchClient(rpc.Watch),
		wdonec:  proxies[c].wdonec,
		kvdonec: proxies[c].kvdonec,
		lpdonec: proxies[c].lpdonec,
	}
	pmu.Unlock()
	return c, nil
}