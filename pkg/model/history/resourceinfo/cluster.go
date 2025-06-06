// Copyright 2024 Google LLC
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

package resourceinfo

import (
	"sync"

	"github.com/GoogleCloudPlatform/khi/pkg/model/history/resourceinfo/noderesource"
	"github.com/GoogleCloudPlatform/khi/pkg/model/history/resourceinfo/resourcelease"
	v1 "k8s.io/api/core/v1"
)

// NodeResourceIDType is enums to represent the type of ids.
type NodeResourceIDType int

const (
	NodeResourceIDTypeUnknown    NodeResourceIDType = 0
	NodeResourceIDTypeContainer  NodeResourceIDType = 1
	NodeResourceIDTypePodSandbox NodeResourceIDType = 2
)

// Cluster stores resource information(node name, Pod IP,Host IP...etc) used from another parser.
// This struct must modify the own fields in thread safe.
type Cluster struct {
	lock      sync.Mutex
	nodeNames map[string]struct{}
	IPs       *resourcelease.ResourceLeaseHistory[*resourcelease.K8sResourceLeaseHolder]
	// records lease history of NEG id to ServiceNetworkEndpointGroup
	NEGs *resourcelease.ResourceLeaseHistory[*resourcelease.K8sResourceLeaseHolder]

	NodeResourceLogBinder *noderesource.LogBinder
	ContainerStatuses     *ContainerStatuses
}

func NewClusterResourceInfo() *Cluster {
	ips := resourcelease.NewResourceLeaseHistory[*resourcelease.K8sResourceLeaseHolder]()
	return &Cluster{
		lock:                  sync.Mutex{},
		nodeNames:             map[string]struct{}{},
		IPs:                   ips,
		NEGs:                  resourcelease.NewResourceLeaseHistory[*resourcelease.K8sResourceLeaseHolder](),
		NodeResourceLogBinder: noderesource.NewLogBinder(),
		ContainerStatuses: &ContainerStatuses{
			lastObservedStatus: make(map[string]v1.ContainerStatus),
		},
	}
}

// AddNode registeres the node name
func (c *Cluster) AddNode(nodeName string) {
	defer c.lock.Unlock()
	c.lock.Lock()
	c.nodeNames[nodeName] = struct{}{}
}

// GetNodes returns copy of the list of node names
func (c *Cluster) GetNodes() []string {
	result := []string{}
	for key := range c.nodeNames {
		result = append(result, key)
	}
	return result
}
