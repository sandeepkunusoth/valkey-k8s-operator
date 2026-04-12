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

package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
)

// getExporterEnvironmentVariables returns the environment variables for the Redis Exporter container.
// The Redis address is set to the Redis host and port, and TLS configuration is set if enabled.
// The function returns list of environment variables.
func getExporterEnvironmentVariables(tlsEnabled bool) []corev1.EnvVar {
	var envVars []corev1.EnvVar
	redisHost := "redis://localhost"
	if tlsEnabled {
		redisHost = "rediss://localhost"
		envVars = append(envVars, corev1.EnvVar{
			Name:  "REDIS_EXPORTER_TLS_CA_CERT_FILE",
			Value: tlsCertMountPath + "/ca.crt",
		})
		envVars = append(envVars, corev1.EnvVar{
			Name:  "REDIS_EXPORTER_TLS_CLIENT_CERT_FILE",
			Value: tlsCertMountPath + "/tls.crt",
		})
		envVars = append(envVars, corev1.EnvVar{
			Name:  "REDIS_EXPORTER_TLS_CLIENT_KEY_FILE",
			Value: tlsCertMountPath + "/tls.key",
		})
	}

	envVars = append(envVars, corev1.EnvVar{
		Name:  "REDIS_ADDR",
		Value: fmt.Sprintf("%s:%d", redisHost, DefaultPort),
	})

	envVars = append(envVars, corev1.EnvVar{
		Name:  "REDIS_EXPORTER_WEB_LISTEN_ADDRESS",
		Value: fmt.Sprintf(":%d", DefaultExporterPort),
	})

	return envVars
}

// generateMetricsExporterContainerDef generates the container definition for the metrics exporter sidecar.
func generateMetricsExporterContainerDef(exporter valkeyiov1alpha1.ExporterSpec, tlsConfig *valkeyiov1alpha1.TLSConfig) corev1.Container {
	exporterImage := DefaultExporterImage
	if exporter.Image != "" {
		exporterImage = exporter.Image
	}
	tlsEnabled := tlsConfig != nil && tlsConfig.Certificate.SecretName != ""
	envVars := getExporterEnvironmentVariables(tlsEnabled)
	var volumeMounts []corev1.VolumeMount
	if tlsEnabled {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      tlsVolumeName,
			MountPath: tlsCertMountPath,
			ReadOnly:  true,
		})
	}
	return corev1.Container{
		Name:         "metrics-exporter",
		Image:        exporterImage,
		Env:          envVars,
		VolumeMounts: volumeMounts,
		Ports: []corev1.ContainerPort{
			{
				Name:          "metrics",
				ContainerPort: DefaultExporterPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		LivenessProbe: &corev1.Probe{
			InitialDelaySeconds: 10,
			PeriodSeconds:       10,
			TimeoutSeconds:      3,
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.FromInt(DefaultExporterPort),
				},
			},
		},
		ReadinessProbe: &corev1.Probe{
			InitialDelaySeconds: 5,
			PeriodSeconds:       1,
			TimeoutSeconds:      3,
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.FromInt(DefaultExporterPort),
				},
			},
		},
		Resources: exporter.Resources,
	}
}
