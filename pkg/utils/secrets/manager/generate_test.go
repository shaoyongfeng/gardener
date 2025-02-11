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

package manager

import (
	"context"
	"strconv"
	"time"

	"github.com/gardener/gardener/pkg/utils"
	secretutils "github.com/gardener/gardener/pkg/utils/secrets"
	"github.com/gardener/gardener/pkg/utils/test"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	kubernetesscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = BeforeSuite(func() {
	DeferCleanup(test.WithVar(&secretutils.GenerateRandomString, secretutils.FakeGenerateRandomString))
	DeferCleanup(test.WithVar(&secretutils.GenerateKey, secretutils.FakeGenerateKey))
})

var _ = Describe("Generate", func() {
	var (
		ctx       = context.TODO()
		namespace = "shoot--foo--bar"
		identity  = "test"

		m          *manager
		fakeClient client.Client
		fakeClock  = clock.NewFakeClock(time.Time{})
	)

	BeforeEach(func() {
		fakeClient = fakeclient.NewClientBuilder().WithScheme(kubernetesscheme.Scheme).Build()

		mgr, err := New(ctx, logr.Discard(), fakeClock, fakeClient, namespace, identity, nil)
		Expect(err).NotTo(HaveOccurred())
		m = mgr.(*manager)
	})

	Describe("#Generate", func() {
		name := "config"

		Context("for non-certificate secrets", func() {
			var config *secretutils.BasicAuthSecretConfig

			BeforeEach(func() {
				config = &secretutils.BasicAuthSecretConfig{
					Name:           name,
					Format:         secretutils.BasicAuthFormatNormal,
					Username:       "foo",
					PasswordLength: 3,
				}
			})

			It("should generate a new secret", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, secret)

				By("verifying internal store reflects changes")
				secretInfos, found := m.getFromStore(name)
				Expect(found).To(BeTrue())
				Expect(secretInfos.current.obj).To(Equal(secret))
				Expect(secretInfos.old).To(BeNil())
				Expect(secretInfos.bundle).To(BeNil())
			})

			It("should maintain the lifetime labels (w/o validity)", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())

				By("reading created secret from system")
				foundSecret := &corev1.Secret{}
				Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(secret), foundSecret)).To(Succeed())

				By("verifying labels")
				Expect(foundSecret.Labels).To(And(
					HaveKeyWithValue("issued-at-time", strconv.FormatInt(fakeClock.Now().Unix(), 10)),
					Not(HaveKey("valid-until-time")),
				))
			})

			It("should maintain the lifetime labels (w/ validity)", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config, Validity(time.Hour))
				Expect(err).NotTo(HaveOccurred())

				By("reading created secret from system")
				foundSecret := &corev1.Secret{}
				Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(secret), foundSecret)).To(Succeed())

				By("verifying labels")
				Expect(foundSecret.Labels).To(And(
					HaveKeyWithValue("issued-at-time", strconv.FormatInt(fakeClock.Now().Unix(), 10)),
					HaveKeyWithValue("valid-until-time", strconv.FormatInt(fakeClock.Now().Add(time.Hour).Unix(), 10)),
				))
			})

			It("should generate a new secret when the config changes", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, secret)

				By("changing secret config and generate again")
				config.PasswordLength = 4
				newSecret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newSecret)

				By("verifying internal store reflects changes")
				secretInfos, found := m.getFromStore(name)
				Expect(found).To(BeTrue())
				Expect(secretInfos.current.obj).To(Equal(newSecret))
				Expect(secretInfos.old).To(BeNil())
				Expect(secretInfos.bundle).To(BeNil())
			})

			It("should generate a new secret when the last rotation initiation time changes", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, secret)

				By("changing last rotation initiation time and generate again")
				mgr, err := New(ctx, logr.Discard(), fakeClock, fakeClient, namespace, identity, map[string]time.Time{name: time.Now()})
				Expect(err).NotTo(HaveOccurred())
				m = mgr.(*manager)

				newSecret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newSecret)

				By("verifying internal store reflects changes")
				secretInfos, found := m.getFromStore(name)
				Expect(found).To(BeTrue())
				Expect(secretInfos.current.obj).To(Equal(newSecret))
				Expect(secretInfos.old).To(BeNil())
				Expect(secretInfos.bundle).To(BeNil())
			})

			It("should store the old secret if rotation strategy is KeepOld", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, secret)

				By("changing secret config and generate again with KeepOld strategy")
				config.PasswordLength = 4
				newSecret, err := m.Generate(ctx, config, Rotate(KeepOld))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newSecret)

				By("verifying internal store reflects changes")
				secretInfos, found := m.getFromStore(name)
				Expect(found).To(BeTrue())
				Expect(secretInfos.current.obj).To(Equal(newSecret))
				Expect(secretInfos.old.obj).To(Equal(withoutTypeMeta(secret)))
				Expect(secretInfos.bundle).To(BeNil())
			})

			It("should not store the old secret even if rotation strategy is KeepOld when old secrets shall be ignored", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, secret)

				By("changing secret config and generate again with KeepOld strategy and ignore old secrets option")
				config.PasswordLength = 4
				newSecret, err := m.Generate(ctx, config, Rotate(KeepOld), IgnoreOldSecrets())
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newSecret)

				By("verifying internal store reflects changes")
				secretInfos, found := m.getFromStore(name)
				Expect(found).To(BeTrue())
				Expect(secretInfos.current.obj).To(Equal(newSecret))
				Expect(secretInfos.old).To(BeNil())
				Expect(secretInfos.bundle).To(BeNil())
			})

			It("should reconcile the secret", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, secret)

				By("marking secret as mutable")
				patch := client.MergeFrom(secret.DeepCopy())
				secret.Immutable = nil
				// ensure that label with empty value is added by another call to Generate
				delete(secret.Labels, "last-rotation-initiation-time")
				Expect(fakeClient.Patch(ctx, secret, patch)).To(Succeed())

				By("changing options and generate again")
				secret, err = m.Generate(ctx, config, Persist())
				Expect(err).NotTo(HaveOccurred())

				By("verifying labels got reconciled")
				foundSecret := &corev1.Secret{}
				Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(secret), foundSecret)).To(Succeed())
				Expect(foundSecret.Labels).To(And(
					HaveKeyWithValue("persist", "true"),
					// ensure that label with empty value is added by another call to Generate
					HaveKeyWithValue("last-rotation-initiation-time", ""),
				))
				Expect(foundSecret.Immutable).To(PointTo(BeTrue()))
			})
		})

		Context("for CA certificate secrets", func() {
			var config *secretutils.CertificateSecretConfig

			BeforeEach(func() {
				config = &secretutils.CertificateSecretConfig{
					Name:       name,
					CommonName: name,
					CertType:   secretutils.CACert,
				}
			})

			It("should generate a new CA secret and a corresponding bundle", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				Expect(secret.Name).To(Equal(name + "-cb09286a"))
				expectSecretWasCreated(ctx, fakeClient, secret)

				By("finding created bundle secret")
				secretList := &corev1.SecretList{}
				Expect(fakeClient.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabels{
					"managed-by":       "secrets-manager",
					"manager-identity": "test",
					"bundle-for":       name,
				})).To(Succeed())
				Expect(secretList.Items).To(HaveLen(1))

				By("verifying internal store reflects changes")
				secretInfos, found := m.getFromStore(name)
				Expect(found).To(BeTrue())
				Expect(secretInfos.current.obj).To(Equal(secret))
				Expect(secretInfos.old).To(BeNil())
				Expect(secretInfos.bundle.obj).To(Equal(withTypeMeta(&secretList.Items[0])))
			})

			It("should maintain the lifetime labels (w/o custom validity)", func() {
				By("generating new secret")
				config.Clock = fakeClock
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())

				By("reading created secret from system")
				foundSecret := &corev1.Secret{}
				Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(secret), foundSecret)).To(Succeed())

				By("verifying labels")
				Expect(foundSecret.Labels).To(And(
					HaveKeyWithValue("issued-at-time", strconv.FormatInt(fakeClock.Now().Unix(), 10)),
					HaveKeyWithValue("valid-until-time", strconv.FormatInt(fakeClock.Now().AddDate(10, 0, 0).Unix(), 10)),
				))
			})

			It("should maintain the lifetime labels (w/ custom validity which is ignored for certificates)", func() {
				By("generating new secret")
				config.Clock = fakeClock
				secret, err := m.Generate(ctx, config, Validity(time.Hour))
				Expect(err).NotTo(HaveOccurred())

				By("reading created secret from system")
				foundSecret := &corev1.Secret{}
				Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(secret), foundSecret)).To(Succeed())

				By("verifying labels")
				Expect(foundSecret.Labels).To(And(
					HaveKeyWithValue("issued-at-time", strconv.FormatInt(fakeClock.Now().Unix(), 10)),
					HaveKeyWithValue("valid-until-time", strconv.FormatInt(fakeClock.Now().AddDate(10, 0, 0).Unix(), 10)),
				))
			})

			It("should generate a new CA secret and ignore the config checksum for its name", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config, IgnoreConfigChecksumForCASecretName())
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, secret)
				Expect(secret.Name).To(Equal(name))
			})

			It("should rotate a CA secret and add old and new to the corresponding bundle", func() {
				By("generating new secret")
				secret, err := m.Generate(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, secret)

				By("storing old bundle secret")
				secretInfos, found := m.getFromStore(name)
				Expect(found).To(BeTrue())
				oldBundleSecret := secretInfos.bundle.obj

				By("changing secret config and generate again")
				mgr, err := New(ctx, logr.Discard(), fakeClock, fakeClient, namespace, identity, map[string]time.Time{name: time.Now()})
				Expect(err).NotTo(HaveOccurred())
				m = mgr.(*manager)

				newSecret, err := m.Generate(ctx, config, Rotate(KeepOld))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newSecret)

				By("finding created bundle secret")
				secretList := &corev1.SecretList{}
				Expect(fakeClient.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabels{
					"managed-by":       "secrets-manager",
					"manager-identity": "test",
					"bundle-for":       name,
				})).To(Succeed())
				Expect(secretList.Items).To(HaveLen(2))

				By("verifying internal store reflects changes")
				secretInfos, found = m.getFromStore(name)
				Expect(found).To(BeTrue())
				Expect(secretInfos.current.obj).To(Equal(newSecret))
				Expect(secretInfos.old.obj).To(Equal(withoutTypeMeta(secret)))
				Expect(secretInfos.bundle.obj).NotTo(PointTo(Equal(oldBundleSecret)))
			})
		})

		Context("for certificate secrets", func() {
			var (
				caName, serverName, clientName       = "ca", "server", "client"
				caConfig, serverConfig, clientConfig *secretutils.CertificateSecretConfig
			)

			BeforeEach(func() {
				caConfig = &secretutils.CertificateSecretConfig{
					Name:       caName,
					CommonName: caName,
					CertType:   secretutils.CACert,
				}
				serverConfig = &secretutils.CertificateSecretConfig{
					Name:                        serverName,
					CommonName:                  serverName,
					CertType:                    secretutils.ServerCert,
					SkipPublishingCACertificate: true,
				}
				clientConfig = &secretutils.CertificateSecretConfig{
					Name:                        clientName,
					CommonName:                  clientName,
					CertType:                    secretutils.ClientCert,
					SkipPublishingCACertificate: true,
				}
			})

			It("should maintain the lifetime labels (w/o custom validity)", func() {
				By("generating new CA secret")
				caSecret, err := m.Generate(ctx, caConfig)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, caSecret)

				By("generating new server secret")
				serverConfig.Clock = fakeClock
				serverSecret, err := m.Generate(ctx, serverConfig, SignedByCA(caName))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, serverSecret)

				By("reading created secret from system")
				foundSecret := &corev1.Secret{}
				Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(serverSecret), foundSecret)).To(Succeed())

				By("verifying labels")
				Expect(foundSecret.Labels).To(And(
					HaveKeyWithValue("issued-at-time", strconv.FormatInt(fakeClock.Now().Unix(), 10)),
					HaveKeyWithValue("valid-until-time", strconv.FormatInt(fakeClock.Now().AddDate(10, 0, 0).Unix(), 10)),
				))
			})

			It("should maintain the lifetime labels (w/ custom validity which is ignored for certificates)", func() {
				By("generating new CA secret")
				caSecret, err := m.Generate(ctx, caConfig)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, caSecret)

				By("generating new server secret")
				serverConfig.Clock = fakeClock
				serverSecret, err := m.Generate(ctx, serverConfig, SignedByCA(caName), Validity(time.Hour))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, serverSecret)

				By("reading created secret from system")
				foundSecret := &corev1.Secret{}
				Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(serverSecret), foundSecret)).To(Succeed())

				By("verifying labels")
				Expect(foundSecret.Labels).To(And(
					HaveKeyWithValue("issued-at-time", strconv.FormatInt(fakeClock.Now().Unix(), 10)),
					HaveKeyWithValue("valid-until-time", strconv.FormatInt(fakeClock.Now().AddDate(10, 0, 0).Unix(), 10)),
				))
			})

			It("should keep the same server cert even when the CA rotates", func() {
				By("generating new CA secret")
				caSecret, err := m.Generate(ctx, caConfig)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, caSecret)

				By("generating new server secret")
				serverSecret, err := m.Generate(ctx, serverConfig, SignedByCA(caName))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, serverSecret)

				By("rotating CA")
				mgr, err := New(ctx, logr.Discard(), fakeClock, fakeClient, namespace, identity, map[string]time.Time{name: time.Now()})
				Expect(err).NotTo(HaveOccurred())
				m = mgr.(*manager)

				newCASecret, err := m.Generate(ctx, caConfig, Rotate(KeepOld))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newCASecret)

				By("get or generate server secret")
				newServerSecret, err := m.Generate(ctx, serverConfig, SignedByCA(caName))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newServerSecret)

				By("verifying server secret is still the same")
				Expect(newServerSecret).To(Equal(withTypeMeta(serverSecret)))
			})

			It("should regenerate the server cert when the CA rotates and the 'UseCurrentCA' option is set", func() {
				By("generating new CA secret")
				caSecret, err := m.Generate(ctx, caConfig)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, caSecret)

				By("generating new server secret")
				serverSecret, err := m.Generate(ctx, serverConfig, SignedByCA(caName, UseCurrentCA))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, serverSecret)

				By("rotating CA")
				mgr, err := New(ctx, logr.Discard(), fakeClock, fakeClient, namespace, identity, map[string]time.Time{caName: time.Now()})
				Expect(err).NotTo(HaveOccurred())
				m = mgr.(*manager)

				newCASecret, err := m.Generate(ctx, caConfig, Rotate(KeepOld))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newCASecret)

				By("get or generate server secret")
				newServerSecret, err := m.Generate(ctx, serverConfig, SignedByCA(caName, UseCurrentCA))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newServerSecret)

				By("verifying server secret is changed")
				Expect(newServerSecret).NotTo(Equal(serverSecret))
			})

			It("should regenerate the client cert when the CA rotates", func() {
				By("generating new CA secret")
				caSecret, err := m.Generate(ctx, caConfig)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, caSecret)

				By("generating new client secret")
				clientSecret, err := m.Generate(ctx, clientConfig, SignedByCA(caName))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, clientSecret)

				By("rotating CA")
				mgr, err := New(ctx, logr.Discard(), fakeClock, fakeClient, namespace, identity, map[string]time.Time{caName: time.Now()})
				Expect(err).NotTo(HaveOccurred())
				m = mgr.(*manager)

				newCASecret, err := m.Generate(ctx, caConfig, Rotate(KeepOld))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newCASecret)

				By("get or generate client secret")
				newClientSecret, err := m.Generate(ctx, clientConfig, SignedByCA(caName))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, newClientSecret)

				By("verifying client secret is changed")
				Expect(newClientSecret).NotTo(Equal(clientSecret))
			})

			It("should also accept ControlPlaneSecretConfigs", func() {
				By("generating new CA secret")
				caSecret, err := m.Generate(ctx, caConfig)
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, caSecret)

				By("generating new control plane secret")
				serverConfig.Clock = fakeClock
				serverConfig.Validity = utils.DurationPtr(1337 * time.Minute)
				controlPlaneSecretConfig := &secretutils.ControlPlaneSecretConfig{
					Name:                    "control-plane-secret",
					CertificateSecretConfig: serverConfig,
					KubeConfigRequests: []secretutils.KubeConfigRequest{{
						ClusterName:   namespace,
						APIServerHost: "some-host",
					}},
				}

				serverSecret, err := m.Generate(ctx, controlPlaneSecretConfig, SignedByCA(caName))
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, serverSecret)

				By("verifying labels")
				Expect(serverSecret.Labels).To(And(
					HaveKeyWithValue("issued-at-time", strconv.FormatInt(fakeClock.Now().Unix(), 10)),
					HaveKeyWithValue("valid-until-time", strconv.FormatInt(fakeClock.Now().Add(*serverConfig.Validity).Unix(), 10)),
				))
			})

			It("should correctly maintain lifetime labels for ControlPlaneSecretConfigs w/o certificate secret configs", func() {
				By("generating new control plane secret")
				cpSecret, err := m.Generate(ctx, &secretutils.ControlPlaneSecretConfig{Name: "control-plane-secret"})
				Expect(err).NotTo(HaveOccurred())
				expectSecretWasCreated(ctx, fakeClient, cpSecret)

				By("verifying labels")
				Expect(cpSecret.Labels).To(And(
					HaveKeyWithValue("issued-at-time", strconv.FormatInt(fakeClock.Now().Unix(), 10)),
					Not(HaveKey("valid-until-time")),
				))
			})
		})

		Context("backwards compatibility", func() {
			Context("etcd encryption key", func() {
				var (
					oldKey    = []byte("old-key")
					oldSecret = []byte("old-secret")
					config    *secretutils.ETCDEncryptionKeySecretConfig
				)

				BeforeEach(func() {
					config = &secretutils.ETCDEncryptionKeySecretConfig{
						Name:         "kube-apiserver-etcd-encryption-key",
						SecretLength: 32,
					}
				})

				It("should generate a new encryption key secret if old secret does not exist", func() {
					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new key and secret were generated")
					Expect(secret.Data["key"]).NotTo(Equal(oldKey))
					Expect(secret.Data["secret"]).NotTo(Equal(oldSecret))
				})

				It("should keep the existing encryption key and secret if old secret still exists", func() {
					oldEncryptionConfiguration := `apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
- providers:
  - aescbc:
      keys:
      - name: ` + string(oldKey) + `
        secret: ` + string(oldSecret) + `
  - identity: {}
  resources:
  - secrets
`

					By("creating existing secret with old encryption configuration")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "etcd-encryption-secret",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: map[string][]byte{"encryption-configuration.yaml": []byte(oldEncryptionConfiguration)},
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old key and secret were kept")
					Expect(secret.Data["key"]).To(Equal(oldKey))
					Expect(secret.Data["secret"]).To(Equal(oldSecret))
				})
			})

			Context("kube-apiserver basic auth", func() {
				var (
					userName    = "admin"
					oldPassword = "old-basic-auth-password"
					config      *secretutils.BasicAuthSecretConfig
				)

				BeforeEach(func() {
					config = &secretutils.BasicAuthSecretConfig{
						Name:           "kube-apiserver-basic-auth",
						Format:         secretutils.BasicAuthFormatCSV,
						Username:       userName,
						PasswordLength: 32,
					}
				})

				It("should generate a new password if old secret does not exist", func() {
					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new password was generated")
					basicAuth, err := secretutils.LoadBasicAuthFromCSV("", secret.Data[secretutils.DataKeyCSV])
					Expect(err).NotTo(HaveOccurred())
					Expect(basicAuth.Password).NotTo(Equal(oldPassword))
				})

				It("should keep the existing password if old secret still exists", func() {
					oldBasicAuth := &secretutils.BasicAuth{
						Format:   secretutils.BasicAuthFormatCSV,
						Username: userName,
						Password: oldPassword,
					}

					By("creating existing secret with old password")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "kube-apiserver-basic-auth",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldBasicAuth.SecretData(),
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old password was kept")
					basicAuth, err := secretutils.LoadBasicAuthFromCSV("", secret.Data[secretutils.DataKeyCSV])
					Expect(err).NotTo(HaveOccurred())
					Expect(basicAuth.Password).To(Equal(oldPassword))
					Expect(secret.Data).ToNot(And(HaveKey("username"), HaveKey("password"), HaveKey("auth")))
				})
			})

			Context("seed monitoring ingress credentials / shoot monitoring ingress credentials (operators)", func() {
				var (
					userName    = "admin"
					oldPassword = "old-basic-auth-password"
					config      *secretutils.BasicAuthSecretConfig
				)

				BeforeEach(func() {
					config = &secretutils.BasicAuthSecretConfig{
						Name:           "observability-ingress",
						Format:         secretutils.BasicAuthFormatNormal,
						Username:       userName,
						PasswordLength: 32,
					}
				})

				It("should generate a new password if old secret does not exist", func() {
					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new password was generated")
					basicAuth, err := secretutils.LoadBasicAuthFromCSV("", secret.Data[secretutils.DataKeyCSV])
					Expect(err).NotTo(HaveOccurred())
					Expect(basicAuth.Password).NotTo(Equal(oldPassword))
				})

				It("should keep the existing password if old secret still exists", func() {
					oldBasicAuth := &secretutils.BasicAuth{
						Format:   secretutils.BasicAuthFormatNormal,
						Username: userName,
						Password: oldPassword,
					}

					By("creating existing secret with old password")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "monitoring-ingress-credentials",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldBasicAuth.SecretData(),
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old password was kept")
					basicAuth, err := secretutils.LoadBasicAuthFromCSV("", secret.Data[secretutils.DataKeyCSV])
					Expect(err).NotTo(HaveOccurred())
					Expect(basicAuth.Password).To(Equal(oldPassword))
					Expect(secret.Data).To(And(HaveKey("username"), HaveKey("password"), HaveKey("auth")))
				})

				It("should keep the existing password if old secret still exists (w/o CSV)", func() {
					oldBasicAuth := &secretutils.BasicAuth{
						Format:   secretutils.BasicAuthFormatNormal,
						Username: userName,
						Password: oldPassword,
					}

					By("creating existing secret with old password")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "monitoring-ingress-credentials",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldBasicAuth.SecretData(),
					}
					delete(existingSecret.Data, secretutils.DataKeyCSV)
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old password was kept")
					basicAuth, err := secretutils.LoadBasicAuthFromCSV("", secret.Data[secretutils.DataKeyCSV])
					Expect(err).NotTo(HaveOccurred())
					Expect(basicAuth.Password).To(Equal(oldPassword))
					Expect(secret.Data).To(And(HaveKey("username"), HaveKey("password"), HaveKey("auth")))
				})
			})

			Context("shoot monitoring ingress credentials (users)", func() {
				var (
					userName    = "admin"
					oldPassword = "old-basic-auth-password"
					config      *secretutils.BasicAuthSecretConfig
				)

				BeforeEach(func() {
					config = &secretutils.BasicAuthSecretConfig{
						Name:           "observability-ingress-users",
						Format:         secretutils.BasicAuthFormatNormal,
						Username:       userName,
						PasswordLength: 32,
					}
				})

				It("should generate a new password if old secret does not exist", func() {
					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new password was generated")
					basicAuth, err := secretutils.LoadBasicAuthFromCSV("", secret.Data[secretutils.DataKeyCSV])
					Expect(err).NotTo(HaveOccurred())
					Expect(basicAuth.Password).NotTo(Equal(oldPassword))
				})

				It("should keep the existing password if old secret still exists", func() {
					oldBasicAuth := &secretutils.BasicAuth{
						Format:   secretutils.BasicAuthFormatNormal,
						Username: userName,
						Password: oldPassword,
					}

					By("creating existing secret with old password")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "monitoring-ingress-credentials-users",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldBasicAuth.SecretData(),
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old password was kept")
					basicAuth, err := secretutils.LoadBasicAuthFromCSV("", secret.Data[secretutils.DataKeyCSV])
					Expect(err).NotTo(HaveOccurred())
					Expect(basicAuth.Password).To(Equal(oldPassword))
					Expect(secret.Data).To(And(HaveKey("username"), HaveKey("password"), HaveKey("auth")))
				})

				It("should keep the existing password if old secret still exists (w/o CSV)", func() {
					oldBasicAuth := &secretutils.BasicAuth{
						Format:   secretutils.BasicAuthFormatNormal,
						Username: userName,
						Password: oldPassword,
					}

					By("creating existing secret with old password")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "monitoring-ingress-credentials-users",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldBasicAuth.SecretData(),
					}
					delete(existingSecret.Data, secretutils.DataKeyCSV)
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old password was kept")
					basicAuth, err := secretutils.LoadBasicAuthFromCSV("", secret.Data[secretutils.DataKeyCSV])
					Expect(err).NotTo(HaveOccurred())
					Expect(basicAuth.Password).To(Equal(oldPassword))
					Expect(secret.Data).To(And(HaveKey("username"), HaveKey("password"), HaveKey("auth")))
				})
			})

			Context("kube-apiserver static token", func() {
				var (
					user1, user2                 = "user1", "user2"
					oldUser1Token, oldUser2Token = "old-static-token-1", "old-static-token-2"
					user1Token                   = secretutils.TokenConfig{
						Username: user1,
						UserID:   user1,
						Groups:   []string{"my-group1"},
					}
					user2Token = secretutils.TokenConfig{
						Username: user2,
						UserID:   user2,
					}

					config *secretutils.StaticTokenSecretConfig
				)

				BeforeEach(func() {
					config = &secretutils.StaticTokenSecretConfig{
						Name: "kube-apiserver-static-token",
						Tokens: map[string]secretutils.TokenConfig{
							user1: user1Token,
							user2: user2Token,
						},
					}
				})

				It("should generate new tokens if old secret does not exist", func() {
					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new tokens were generated")
					staticToken, err := secretutils.LoadStaticTokenFromCSV("", secret.Data[secretutils.DataKeyStaticTokenCSV])
					Expect(err).NotTo(HaveOccurred())
					for _, token := range staticToken.Tokens {
						if token.Username == user1 {
							Expect(token.Token).NotTo(Equal(oldUser1Token))
						}
						if token.Username == user2 {
							Expect(token.Token).NotTo(Equal(oldUser2Token))
						}
					}
				})

				It("should generate keep the old tokens if old secret does still exist", func() {
					oldBasicAuth := &secretutils.StaticToken{
						Tokens: []secretutils.Token{
							{
								Username: user1Token.Username,
								UserID:   user1Token.UserID,
								Groups:   user1Token.Groups,
								Token:    oldUser1Token,
							},
							{
								Username: user2Token.Username,
								UserID:   user2Token.UserID,
								Groups:   user2Token.Groups,
								Token:    oldUser2Token,
							},
						},
					}

					By("creating existing secret with old password")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "static-token",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldBasicAuth.SecretData(),
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new tokens were generated")
					staticToken, err := secretutils.LoadStaticTokenFromCSV("", secret.Data[secretutils.DataKeyStaticTokenCSV])
					Expect(err).NotTo(HaveOccurred())
					for _, token := range staticToken.Tokens {
						if token.Username == user1 {
							Expect(token.Token).To(Equal(oldUser1Token))
						}
						if token.Username == user2 {
							Expect(token.Token).To(Equal(oldUser2Token))
						}
					}
				})
			})

			Context("ssh-keypair", func() {
				var (
					oldData = map[string][]byte{
						"id_rsa":     []byte("private-key"),
						"id_rsa.pub": []byte("public key"),
					}
					config *secretutils.RSASecretConfig
				)

				BeforeEach(func() {
					config = &secretutils.RSASecretConfig{
						Name:       "ssh-keypair",
						Bits:       4096,
						UsedForSSH: true,
					}
				})

				It("should generate a new ssh keypair if old secret does not exist", func() {
					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new keypair was generated")
					Expect(secret.Data).NotTo(Equal(oldData))
				})

				It("should keep the existing ssh keypair if old secret still exists", func() {
					By("creating existing secret with old password")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "ssh-keypair",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldData,
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old password was kept")
					Expect(secret.Data).To(Equal(oldData))
				})

				It("should make the manager adopt the old ssh keypair if it exists", func() {
					By("creating existing secret with old password")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "ssh-keypair",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldData,
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("creating existing old secret")
					existingOldSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "ssh-keypair.old",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
					}
					Expect(fakeClient.Create(ctx, existingOldSecret)).To(Succeed())

					By("generating secret")
					_, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old ssh keypair was adopted")
					Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(existingOldSecret), existingOldSecret)).To(Succeed())
					Expect(existingOldSecret.Immutable).To(PointTo(BeTrue()))
					Expect(existingOldSecret.Labels).To(Equal(map[string]string{
						"name":                          "ssh-keypair",
						"managed-by":                    "secrets-manager",
						"manager-identity":              "test",
						"persist":                       "true",
						"last-rotation-initiation-time": "",
					}))
				})
			})

			Context("service account key", func() {
				var (
					oldData = map[string][]byte{"id_rsa": []byte("some-old-key")}
					config  *secretutils.RSASecretConfig
				)

				BeforeEach(func() {
					config = &secretutils.RSASecretConfig{
						Name: "service-account-key",
						Bits: 4096,
					}
				})

				It("should generate a new key if old secret does not exist", func() {
					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new key was generated")
					Expect(secret.Data).NotTo(Equal(oldData))
				})

				It("should keep the existing key if old secret still exists", func() {
					By("creating existing secret with old key")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "service-account-key",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: oldData,
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying old password was kept")
					Expect(secret.Data).To(Equal(oldData))
				})
			})

			Context("ca-client", func() {
				var (
					caClusterKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
...
-----END RSA PRIVATE KEY-----
`)
					caClusterCert = []byte(`-----BEGIN CERTIFICATE-----
MIIC9jCCAd6gAwIBAgIUEoEOCJXk3WYh/R86QU6tl21Inc4wDQYJKoZIhvcNAQEL
BQAwEzERMA8GA1UEAxMIZ2FyZGVuZXIwHhcNMjExMDE4MTMwNzAwWhcNMjYxMDE3
MTMwNzAwWjATMREwDwYDVQQDEwhnYXJkZW5lcjCCASIwDQYJKoZIhvcNAQEBBQAD
ggEPADCCAQoCggEBAN/p2ouZ/IEN1AcSeZcStVqovo5Z27ZMFYBVTbcTd3L1oKhh
KuO7Z8UORXZJKUX313CqMRE9DeB0FoDOeiOUFxn41Cz+efI7FxxYKi1CqZOPWl4d
Ihrsh2+wsbxgIUEVDIMO//HBE9pugqn9DTuwpg2EjT67gXttJgLyOcNkeQPE6V2y
jlAoYxJuOLWt/Np82qqAOnioZS2yDvzwXrAFCFK1tqSAt9A8W+p/XgWTTqdWPF9W
HOjzFg+Ux+VyasSCM2Cchot14/HwH3SBVXvi0SnPjpNeuQqDlQAEqnruMOIOObVw
swOcXn5dFck+7vrCCdJnOKIZ+WO+DrngMcOvkc8CAwEAAaNCMEAwDgYDVR0PAQH/
BAQDAgEGMA8GA1UdEwEB/wQFMAMBAf8wHQYDVR0OBBYEFGpfM5/ePMXpslUv/A/K
6yjQUjMjMA0GCSqGSIb3DQEBCwUAA4IBAQCAUA+e4JuaEDz2gvIAleemuQ/9ylFq
/XmzevAjqXSndKmkMTwOxvPWAA3xLJjvowZy0i5tPA/hFh5gIknReL4kGINGnCaL
50ZbcClvlm1ClqUw97S0qBNZQP4xGa3ChkpKERs8liXu613xNqIgib1+0FtfTvGm
qSHwwAI6pMbxxzYBFftp0YLBjpwDHZOhBAQcVPtld8Cce/Md6bNMNgqtTkDVWRXV
TrZURcfizB5ipQRgOCiXaX/U0qxYWbG0Xrrvt869wNnl8DKfx5YtdCeHhT74Go0A
FskcKs088h3kZh8sc8pG25SCwKdEXXh7ufO3aYtEbViSAQbqIixNVdRO
-----END CERTIFICATE-----
`)
					config *secretutils.CertificateSecretConfig
				)

				BeforeEach(func() {
					config = &secretutils.CertificateSecretConfig{
						Name:       "ca-client",
						CommonName: "kubernetes-client",
						CertType:   secretutils.CACert,
					}
				})

				It("should generate a new CA certificate if the cluster CA does not exist", func() {
					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying new CA certificate was generated")
					Expect(secret.Data[secretutils.DataKeyCertificateCA]).NotTo(Equal(caClusterCert))
					Expect(secret.Data[secretutils.DataKeyPrivateKeyCA]).NotTo(Equal(caClusterKey))
				})

				It("should use the cluster CA certificate if cluster CA exists", func() {
					By("creating cluster CA secret")
					existingSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "ca",
							Namespace: namespace,
						},
						Type: corev1.SecretTypeOpaque,
						Data: map[string][]byte{
							"ca.crt": caClusterCert,
							"ca.key": caClusterKey,
						},
					}
					Expect(fakeClient.Create(ctx, existingSecret)).To(Succeed())

					By("generating secret")
					secret, err := m.Generate(ctx, config)
					Expect(err).NotTo(HaveOccurred())

					By("verifying cluster CA certificate is used")
					Expect(secret.Data[secretutils.DataKeyCertificateCA]).To(Equal(caClusterCert))
					Expect(secret.Data[secretutils.DataKeyPrivateKeyCA]).To(Equal(caClusterKey))
				})
			})
		})
	})
})

func expectSecretWasCreated(ctx context.Context, fakeClient client.Client, secret *corev1.Secret) {
	foundSecret := &corev1.Secret{}
	Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(secret), foundSecret)).To(Succeed())

	Expect(foundSecret).To(Equal(withTypeMeta(secret)))
}

func withTypeMeta(obj *corev1.Secret) *corev1.Secret {
	secret := obj.DeepCopy()
	secret.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}
	return secret
}

func withoutTypeMeta(obj *corev1.Secret) *corev1.Secret {
	secret := obj.DeepCopy()
	secret.TypeMeta = metav1.TypeMeta{}
	return secret
}
