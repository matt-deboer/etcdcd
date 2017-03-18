etcdcd
==============

[![Build Status](https://travis-ci.org/matt-deboer/etcdcd.svg?branch=master)](https://travis-ci.org/matt-deboer/etcdcd)
[![Docker Pulls](https://img.shields.io/docker/pulls/mattdeboer/etcdcd.svg)](https://hub.docker.com/r/mattdeboer/etcdcd/)

Assists in creation/joining/pruning of an etcd (2.x) cluster using the `static` discovery mechanism. 
Supports `aws` (ASG-based masters) and `vsphere` deployments currently.


## Motivation

Use this binary (or docker image) to produce appropriate environment variable values for
`ETCD_NAME`, `ETCD_INITIAL_CLUSTER`, `ETCD_INITIAL_CLUSTER_STATE`, and `ETCD_PROXY` used in [static](https://coreos.com/etcd/docs/latest/op-guide/clustering.html#static) cluster configuration mode.

The values are generated using the following algorithm:

1. platform-specific discovery of the set of expected cluster memebers' names and peer & client URLs
1. check with the expected members for a pre-existing cluster

    - if an existing cluster is found, and we are one of the expected members, join the existing cluster--attempting to
      evict any current cluster members that are not in the set of expected members
    - if no existing cluster is found and we are one of the expected members, form a new cluster
    - if we are not one of the expected members, and proxy mode is specified, proxy the new or existing cluster
    - otherwise, report invalid cluster state 


