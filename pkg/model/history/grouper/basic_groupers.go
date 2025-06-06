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

package grouper

import (
	common_grouper "github.com/GoogleCloudPlatform/khi/pkg/common/grouper"
	"github.com/GoogleCloudPlatform/khi/pkg/log"
)

var AllIndependentLogGrouper LogGrouper = common_grouper.NewBasicGrouper(func(l *log.Log) string {
	return l.ID
})

var AllDependentLogGrouper LogGrouper = common_grouper.NewBasicGrouper(func(log *log.Log) string {
	return ""
})

func NewSingleStringFieldKeyLogGrouper(keyPath string) LogGrouper {
	return common_grouper.NewBasicGrouper(func(log *log.Log) string {
		return log.ReadStringOrDefault(keyPath, "")
	})
}
