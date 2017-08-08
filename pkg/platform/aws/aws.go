package aws

import (
	"fmt"
	"io"

	log "github.com/Sirupsen/logrus"
	"github.com/matt-deboer/etcdcd/pkg/platform"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	etcd "github.com/coreos/etcd/client"
)

func init() {
	platform.Register("aws", func(config io.Reader) (platform.Platform, error) {
		return newAWS()
	})
}

// AWS is a Platform implementation for AWS
type AWS struct {
	localInstanceID string
	region          *string
	credentials     *credentials.Credentials
}

func newAWS() (*AWS, error) {
	svc := ec2metadata.New(session.New(&aws.Config{}))
	doc, err := svc.GetInstanceIdentityDocument()
	if err != nil {
		return nil, err
	}
	aws := &AWS{
		localInstanceID: doc.InstanceID,
		region:          &doc.Region,
		credentials: credentials.NewChainCredentials(
			[]credentials.Provider{
				&credentials.EnvProvider{},
				&ec2rolecreds.EC2RoleProvider{
					Client: ec2metadata.New(session.New(&aws.Config{})),
				},
				&credentials.SharedCredentialsProvider{},
			}),
	}
	return aws, nil
}

// ExpectedMembers returns a list of members that should form the cluster
func (a *AWS) ExpectedMembers(
	memberFilter string, clientScheme string, clientPort int, serverScheme string, serverPort int) ([]etcd.Member, error) {

	sess := session.New(&aws.Config{
		Region:      a.region,
		Credentials: a.credentials,
	})
	instanceIDs, err := a.getASGInstanceIDs(sess, memberFilter)
	if err != nil {
		return nil, err
	}
	if log.GetLevel() >= log.DebugLevel {
		instances := ""
		for _, i := range instanceIDs {
			instances += "," + *i
		}
		log.Debugf("ExpectedMembers: for asg %s, found the following instances: [%s]", memberFilter, instances[1:])
	}

	svc := ec2.New(sess)
	params := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []*string{aws.String("running")},
			},
		},
		InstanceIds: instanceIDs,
	}
	resp, err := svc.DescribeInstances(params)
	if err != nil {
		return nil, err
	}

	expectedMembers := make([]etcd.Member, 0, len(resp.Reservations[0].Instances))
	for _, r := range resp.Reservations {
		for _, i := range r.Instances {
			member := etcd.Member{
				Name:       *i.InstanceId,
				PeerURLs:   []string{fmt.Sprintf("%s://%s:%d", serverScheme, *i.PrivateIpAddress, serverPort)},
				ClientURLs: []string{fmt.Sprintf("%s://%s:%d", clientScheme, *i.PrivateIpAddress, clientPort)},
			}
			expectedMembers = append(expectedMembers, member)
		}
	}

	return expectedMembers, nil
}

// LocalInstanceName returns a list of members that should form the cluster
func (a *AWS) LocalInstanceName() string {
	return a.localInstanceID
}

func (a *AWS) getASGInstanceIDs(sess *session.Session, asgName string) ([]*string, error) {
	svc := autoscaling.New(sess)

	params := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String(asgName)},
		MaxRecords:            aws.Int64(1),
	}
	resp, err := svc.DescribeAutoScalingGroups(params)

	if err != nil {
		return nil, err
	}

	if resp.AutoScalingGroups == nil || len(resp.AutoScalingGroups) == 0 {
		return nil, fmt.Errorf("No autoscaling groups were found matching the name '%s'", asgName)
	}

	instanceIDs := make([]*string, 0, len(resp.AutoScalingGroups[0].Instances))
	for _, i := range resp.AutoScalingGroups[0].Instances {
		instanceIDs = append(instanceIDs, i.InstanceId)
	}
	return instanceIDs, nil
}
