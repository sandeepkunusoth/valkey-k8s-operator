//go:build e2e
// +build e2e

/*
Copyright 2025 Valkey Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"valkey.io/valkey-operator/test/utils"
)

var _ = Describe("Valkey TLS", Label("tls"), func() {
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			utils.CollectDebugInfo(namespace)
		}
	})

	Context("TLS is enabled", func() {

		It("should successfully provision Valkey with TLS using a cert-manager self-signed cert", func() {
			By("creating a cert-manager Issuer and Certificate")
			issuerYaml := `
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: selfsigned-issuer
  namespace: default
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: valkey-server-cert
  namespace: default
spec:
  secretName: valkey-server-tls
  issuerRef:
    name: selfsigned-issuer
    kind: Issuer
  commonName: valkey-server
  dnsNames:
  - localhost
`
			issuerFile := filepath.Join(os.TempDir(), "issuer.yaml")
			err := os.WriteFile(issuerFile, []byte(issuerYaml), 0644)
			Expect(err).NotTo(HaveOccurred(), "Failed to write issuer manifest")
			defer os.Remove(issuerFile)

			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "apply", "-f", issuerFile)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to create Issuer and Certificate")
			}).Should(Succeed())
			defer func() {
				cmd := exec.Command("kubectl", "delete", "-f", issuerFile, "--ignore-not-found=true")
				utils.Run(cmd)
			}()

			By("waiting for Certificate to be ready and Secret to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", "valkey-server-tls", "-n", "default")
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Secret valkey-server-tls not found yet")
			}, "1m", "5s").Should(Succeed())

			By("creating a ValkeyCluster with TLS enabled and pointing to the created secret")
			valkeyName := "valkey-cluster-tls-valid"
			valkeyYaml := fmt.Sprintf(`
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
metadata:
  name: %s
spec:
  shards: 3
  replicas: 1
  tls:
    enabled: true
    existingSecret: valkey-server-tls
    cert: tls.crt
    key: tls.key
    ca: tls.crt
`, valkeyName)

			manifestFile := filepath.Join(os.TempDir(), fmt.Sprintf("%s.yaml", valkeyName))
			err = os.WriteFile(manifestFile, []byte(valkeyYaml), 0644)
			Expect(err).NotTo(HaveOccurred(), "Failed to write manifest file")
			defer os.Remove(manifestFile)

			cmd := exec.Command("kubectl", "create", "-f", manifestFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ValkeyCluster CR")
			defer func() {
				cmd := exec.Command("kubectl", "delete", "valkeycluster", valkeyName, "--ignore-not-found=true")
				utils.Run(cmd)
			}()

			By("validating that the pods are running")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods",
					"-l", fmt.Sprintf("app.kubernetes.io/instance=%s", valkeyName),
					"-o", "jsonpath={.items[*].status.phase}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Running"))
			}).Should(Succeed())

			By("validating the status condition ClusterHealthy")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "valkeycluster", valkeyName,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("ClusterHealthy"))
			}).Should(Succeed())

			By("Getting pod name for metrics verification")
			var podName string
			Eventually(func(g Gomega) {
				args := []string{
					"get", "pods", "-l", "app.kubernetes.io/instance=" + valkeyName,
					"-o", "jsonpath={.items[0].metadata.name}",
				}
				cmd := exec.Command("kubectl", args...)
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get pod name")
				g.Expect(out).NotTo(BeEmpty(), "Pod name should not be empty")
				podName = out
			}).Should(Succeed())

			By("Getting pod IP for metrics verification")
			var podIP string
			Eventually(func(g Gomega) {
				args := []string{
					"get", "pods", podName,
					"-o", "jsonpath={.status.podIP}",
				}
				cmd := exec.Command("kubectl", args...)
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get pod IP")
				g.Expect(out).NotTo(BeEmpty(), "Pod IP should not be empty")
				podIP = out
			}).Should(Succeed())

			By("Creating a curl pod to test metrics")
			curlPodName := "curl-metrics-" + valkeyName
			cmd = exec.Command("kubectl", "run", curlPodName, "--image=curlimages/curl:latest", "--restart=Never",
				"--overrides",
				`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["sleep 3600"],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}]
					}
				}`)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl pod")
			defer func() {
				cmd := exec.Command("kubectl", "delete", "pod", curlPodName, "--ignore-not-found=true", "--wait=false")
				_, _ = utils.Run(cmd)
			}()

			By("Waiting for the curl pod to be running")
			Eventually(func(g Gomega) {
				args := []string{
					"get", "pods", curlPodName,
					"-o", "jsonpath={.status.phase}",
				}
				cmd := exec.Command("kubectl", args...)
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get curl pod status")
				g.Expect(out).To(Equal("Running"), "Curl pod should be running")
			}).Should(Succeed())

			By("verifying common Valkey metrics are exposed via TLS exporter")
			Eventually(func(g Gomega) {
				url := fmt.Sprintf("http://%s:9121/metrics", podIP)
				cmd := exec.Command("kubectl", "exec", curlPodName, "--", "curl", "-s", url)
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get metrics")
				// Check that redis_up is 1 (indicating successful connection)
				g.Expect(out).To(MatchRegexp(`redis_up\s+1`), "redis_up should be 1 (healthy)")
				// Verify essential Prometheus metrics are present
				g.Expect(out).To(ContainSubstring("redis_up"), "Should contain redis_up metric")
			}).Should(Succeed())
		})

		It("should fail when TLS is enabled with missing secret", func() {
			By("creating a ValkeyCluster with missing secret")
			valkeyName := "valkey-cluster-tls-missing-secret"
			valkeyYaml := fmt.Sprintf(`
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
metadata:
  name: %s
spec:
  shards: 1
  replicas: 0
  tls:
    enabled: true
    existingSecret: "non-existent-secret"
`, valkeyName)

			manifestFile := filepath.Join(os.TempDir(), fmt.Sprintf("%s.yaml", valkeyName))
			err := os.WriteFile(manifestFile, []byte(valkeyYaml), 0644)
			Expect(err).NotTo(HaveOccurred(), "Failed to write manifest file")
			defer os.Remove(manifestFile)

			By("applying the CR")
			cmd := exec.Command("kubectl", "create", "-f", manifestFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ValkeyCluster CR")

			By("validating the status condition MissingTLSSecret")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "valkeycluster", valkeyName,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("MissingTLSSecret"))
			}).Should(Succeed())

			By("validating the error message for missing secret")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "valkeycluster", valkeyName,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].message}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("failed to get TLS secret"))
			}).Should(Succeed())

			By("Cleaning up test resources")
			cmd = exec.Command("kubectl", "delete", "valkeycluster", valkeyName, "--ignore-not-found=true")
			utils.Run(cmd)
		})

		It("should fail when TLS is enabled without specifying secret name", func() {
			By("creating a ValkeyCluster without specifying secret name")
			valkeyName := "valkey-cluster-tls-no-secret-name"
			valkeyYaml := fmt.Sprintf(`
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
metadata:
  name: %s
spec:
  shards: 1
  replicas: 0
  tls:
    enabled: true
`, valkeyName)

			manifestFile := filepath.Join(os.TempDir(), fmt.Sprintf("%s.yaml", valkeyName))
			err := os.WriteFile(manifestFile, []byte(valkeyYaml), 0644)
			Expect(err).NotTo(HaveOccurred(), "Failed to write manifest file")
			defer os.Remove(manifestFile)

			By("applying the CR")
			cmd := exec.Command("kubectl", "create", "-f", manifestFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ValkeyCluster CR")

			By("validating the status condition InvalidTLSSecret")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "valkeycluster", valkeyName,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("InvalidTLSSecret"))
			}).Should(Succeed())

			By("validating the error message for missing secret name")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "valkeycluster", valkeyName,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].message}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("tls.existingSecret is required"))
			}).Should(Succeed())

			By("Cleaning up test resources")
			cmd = exec.Command("kubectl", "delete", "valkeycluster", valkeyName, "--ignore-not-found=true")
			utils.Run(cmd)
		})
	})

	Context("TLS is not enabled", func() {
		It("should successfully start up when TLS is not enabled", func() {
			By("creating a ValkeyCluster with TLS disabled")
			valkeyName := "valkey-cluster-tls-disabled"
			valkeyYaml := fmt.Sprintf(`
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
metadata:
  name: %s
spec:
  shards: 1
  replicas: 0
  tls:
    enabled: false
`, valkeyName)

			manifestFile := filepath.Join(os.TempDir(), fmt.Sprintf("%s.yaml", valkeyName))
			err := os.WriteFile(manifestFile, []byte(valkeyYaml), 0644)
			Expect(err).NotTo(HaveOccurred(), "Failed to write manifest file")
			defer os.Remove(manifestFile)

			By("applying the CR")
			cmd := exec.Command("kubectl", "create", "-f", manifestFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ValkeyCluster CR")

			By("validating that the pods are running")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods",
					"-l", fmt.Sprintf("app.kubernetes.io/instance=%s", valkeyName),
					"-o", "jsonpath={.items[*].status.phase}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Running"))
			}).Should(Succeed())

			By("validating the status condition ClusterHealthy")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "valkeycluster", valkeyName,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("ClusterHealthy"))
			}).Should(Succeed())

			By("Cleaning up test resources")
			cmd = exec.Command("kubectl", "delete", "valkeycluster", valkeyName, "--ignore-not-found=true")
			utils.Run(cmd)
		})
	})
})
