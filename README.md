etcdcd
==============

[![Build Status](https://travis-ci.org/matt-deboer/etcdcd.svg?branch=master)](https://travis-ci.org/matt-deboer/etcdcd)
[![Docker Pulls](https://img.shields.io/docker/pulls/mattdeboer/etcdcd.svg)](https://hub.docker.com/r/mattdeboer/etcdcd/)

**etcd cluster discovery**

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

## Usage

```
NAME:
   etcdcd -
    Dynamically discover etcd cluster membership for a specific platform.
    Useed to produce appropriate environment variable values for
    'ETCD_NAME', 'ETCD_INITIAL_CLUSTER', 'ETCD_INITIAL_CLUSTER_STATE',
    and 'ETCD_PROXY' for use use in static cluster configuration mode.


USAGE:
   etcdcd [global options] command [command options] [arguments...]

VERSION:
   v0.1

COMMANDS:
     help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --platform value              The platform ('aws' or 'vsphere') [$ETCDCD_PLATFORM]
   --platform-config-file value  The platform config file; for platform 'aws', this is
                                       unnecessary/ignored when using an instance role; for platform
                                       'vsphere', this file uses the same format as required by the
                                       vsphere kubernetes cloud provider [$ETCDCD_PLATFORM_CONFIG]
   --output-file value           The path to the output file where results will be
                                       written; uses STDOUT if not specified [$ETCDCD_OUTPUT_FILE]
   --client-port value           The port advertised for etcd client access (default: 2379) [$ETCDCD_CLIENT_PORT]
   --server-port value           The port advertised for etcd client access (default: 2380) [$ETCDCD_SERVER_PORT]
   --client-scheme value         The scheme for etcd client access urls (default: "http") [$ETCDCD_CLIENT_SCHEME]
   --server-scheme value         The scheme for etcd server access urls (default: "http") [$ETCDCD_SERVER_SCHEME]
   --proxy                       Whether to enable proxy mode [$ETCDCD_PROXY_MODE]
   --master-names value          The naming pattern used to locate the masters; for platform 'aws',
                                       this will be the name of the masters autoscaling group;
                                       for platform 'vsphere', this will be a name-glob matching the vm names
                                       of the masters (e.g., 'k8s-master-*') [$ETCDCD_MASTER_NAMES]
   --dry-run                     Don't perform any changes; instead log what would have been done [$ETCDCD_DRY_RUN]
   --verbose, -V                 Log extra information about steps taken [$ETCDCD_VERBOSE]
   --ignore-naming-mismatch      Whether to ignore names (and only compare peer urls) when
                                       looking for existing members [$ETCDCD_IGNORE_NAMING_MISMATCH]
   --help, -h                    show help
   --version, -v                 print the version
```
