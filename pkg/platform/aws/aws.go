package aws

import (
	"fmt"
	"io"

	"github.com/matt-deboer/etcdcd/pkg/platform"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	etcd "github.com/coreos/etcd/client"
)

func init() {
	platform.Register("aws", func(config io.Reader) (platform.Platform, error) {
		return &AWS{}, nil
	})
}

// AWS is a Platform implementation for AWS
type AWS struct {
	localInstanceID string
}

func newAWS() (*AWS, error) {
	sess := session.Must(session.NewSession())
	svc := ec2metadata.New(sess)
	doc, err := svc.GetInstanceIdentityDocument()
	if err != nil {
		return nil, err
	}
	aws := &AWS{
		localInstanceID: doc.InstanceID,
	}
	return aws, nil
}

// ExpectedMembers returns a list of members that should form the cluster
func (a *AWS) ExpectedMembers(
	memberFilter string, clientScheme string, clientPort int, serverScheme string, serverPort int) ([]etcd.Member, error) {

	sess := session.Must(session.NewSession())
	instanceIDs, err := getASGInstanceIDs(sess, memberFilter)
	if err != nil {
		return nil, err
	}

	svc := ec2.New(sess)

	diParams := &ec2.DescribeInstancesInput{
		Filters:     []*ec2.Filter{},
		InstanceIds: instanceIDs,
		MaxResults:  aws.Int64(int64(len(instanceIDs))),
	}
	resp, err := svc.DescribeInstances(diParams)
	if err != nil {
		return nil, err
	}

	expectedMembers := make([]etcd.Member, 0, len(resp.Reservations[0].Instances))
	for _, i := range resp.Reservations[0].Instances {
		member := etcd.Member{
			Name:       *i.InstanceId,
			PeerURLs:   []string{fmt.Sprintf("%s://%s:%d", serverScheme, *i.PrivateIpAddress, serverPort)},
			ClientURLs: []string{fmt.Sprintf("%s://%s:%d", clientScheme, *i.PrivateIpAddress, clientPort)},
		}
		expectedMembers = append(expectedMembers, member)
	}

	return expectedMembers, nil
}

// LocalInstanceName returns a list of members that should form the cluster
func (a *AWS) LocalInstanceName() string {
	return a.localInstanceID
}

func getASGInstanceIDs(sess *session.Session, asgName string) ([]*string, error) {
	svc := autoscaling.New(sess)

	params := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String(asgName)},
		MaxRecords:            aws.Int64(1),
	}
	resp, err := svc.DescribeAutoScalingGroups(params)

	if err != nil {
		return nil, err
	}

	instanceIDs := make([]*string, 0, len(resp.AutoScalingGroups[0].Instances))
	for _, i := range resp.AutoScalingGroups[0].Instances {
		instanceIDs = append(instanceIDs, i.InstanceId)
	}
	return instanceIDs, nil
}
