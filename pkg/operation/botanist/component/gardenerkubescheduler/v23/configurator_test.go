// Copyright (c) 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v23_test

import (
	"testing"

	v23 "github.com/gardener/gardener/pkg/operation/botanist/component/gardenerkubescheduler/v23"
	"github.com/gardener/gardener/third_party/kube-scheduler/v23/v1beta3"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/pointer"
)

func TestConfigurator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Botanist Component GardenerKubeScheduler v23 Suite")
}

var _ = Describe("NewConfigurator", func() {
	It("should not return nil", func() {
		c, err := v23.NewConfigurator("baz", "test", &v1beta3.KubeSchedulerConfiguration{})

		Expect(err).NotTo(HaveOccurred())
		Expect(c).NotTo(BeNil())
	})
})

var _ = Describe("Config", func() {
	var output string

	JustBeforeEach(func() {
		c, err := v23.NewConfigurator("baz", "test", &v1beta3.KubeSchedulerConfiguration{
			Profiles: []v1beta3.KubeSchedulerProfile{
				{
					SchedulerName: pointer.String("test"),
				},
			},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(c).NotTo(BeNil())

		output, err = c.Config()
		Expect(err).NotTo(HaveOccurred())
	})

	It("returns correct config", func() {
		Expect(output).To(Equal(`apiVersion: kubescheduler.config.k8s.io/v1beta3
clientConnection:
  acceptContentTypes: ""
  burst: 0
  contentType: ""
  kubeconfig: ""
  qps: 0
kind: KubeSchedulerConfiguration
leaderElection:
  leaderElect: true
  leaseDuration: 15s
  renewDeadline: 10s
  resourceLock: leases
  resourceName: baz
  resourceNamespace: test
  retryPeriod: 2s
profiles:
- schedulerName: test
`))
	})
})
