/*
Copyright 2019 The KubeOne Authors.

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
	"bytes"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	k1api "github.com/kubermatic/kubeone/pkg/apis/kubeone/v1beta1"
	"github.com/kubermatic/kubeone/test/e2e/testutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kyaml "sigs.k8s.io/yaml"
)

// Kubeone is wrapper around KubeOne CLI
type Kubeone struct {
	// Dir is a temporary directory for storing test files (e.g. tf.json)
	Dir string
	// ConfigurationFilePath is a path to the KubeOneCluster manifest
	ConfigurationFilePath string
}

// NewKubeone creates and initializes the Kubeone structure
func NewKubeone(kubeoneDir, configurationFilePath string) *Kubeone {
	return &Kubeone{
		Dir:                   kubeoneDir,
		ConfigurationFilePath: configurationFilePath,
	}
}

// CreateConfig creates a KubeOneCluster manifest
func (k1 *Kubeone) CreateConfig(
	kubernetesVersion string,
	providerName string,
	providerExternal bool,
	clusterNetworkPod string,
	clusterNetworkService string,
	credentialsFile string,
) error {
	k1Cluster := k1api.KubeOneCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kubeone.io/v1beta1",
			Kind:       "KubeOneCluster",
		},
	}

	k1api.SetObjectDefaults_KubeOneCluster(&k1Cluster)

	k1Cluster.CloudProvider = k1api.CloudProviderSpec{
		External: providerExternal,
	}
	switch providerName {
	case "aws":
		k1Cluster.CloudProvider.AWS = &k1api.AWSSpec{}
	case "azure":
		k1Cluster.CloudProvider.Azure = &k1api.AzureSpec{}
	case "digitalocean":
		k1Cluster.CloudProvider.DigitalOcean = &k1api.DigitalOceanSpec{}
	case "gce":
		k1Cluster.CloudProvider.GCE = &k1api.GCESpec{}
	case "hetzner":
		k1Cluster.CloudProvider.Hetzner = &k1api.HetznerSpec{}
	case "openstack":
		k1Cluster.CloudProvider.Openstack = &k1api.OpenstackSpec{}
	case "packet":
		k1Cluster.CloudProvider.Packet = &k1api.PacketSpec{}
	case "vsphere":
		k1Cluster.CloudProvider.Vsphere = &k1api.VsphereSpec{}
	case "none":
		k1Cluster.CloudProvider.None = &k1api.NoneSpec{}
	default:
		return errors.Errorf("unknown cloud provider %q", providerName)
	}

	k1Cluster.Versions = k1api.VersionConfig{
		Kubernetes: kubernetesVersion,
	}

	k1Cluster.ClusterNetwork = k1api.ClusterNetworkConfig{
		PodSubnet:     clusterNetworkPod,
		ServiceSubnet: clusterNetworkService,
	}

	if credentialsFile != "" {
		ymlbuf, err := ioutil.ReadFile(credentialsFile)
		if err != nil {
			return errors.Wrap(err, "unable to read credentials file")
		}

		credentials := map[string]string{}
		if err = yaml.Unmarshal(ymlbuf, &credentials); err != nil {
			return errors.Wrap(err, "unable to unmarshal credentials file from yaml")
		}

		k1Cluster.CloudProvider.CloudConfig = credentials["cloudConfig"]
	}

	k1Config, err := kyaml.Marshal(&k1Cluster)
	if err != nil {
		return errors.Wrap(err, "unable to marshal kubeone KubeOneCluster")
	}

	err = ioutil.WriteFile(k1.ConfigurationFilePath, k1Config, 0600)
	return errors.Wrap(err, "failed to write KubeOne configuration manifest")
}

// Install runs 'kubeone install' command to provision the cluster
func (k1 *Kubeone) Install(tfJSON string, installFlags []string) error {
	err := k1.storeTFJson(tfJSON)
	if err != nil {
		return err
	}

	flags := []string{"install",
		"--tfjson", "tf.json",
		"--manifest", k1.ConfigurationFilePath}
	if len(installFlags) != 0 {
		flags = append(flags, installFlags...)
	}

	err = k1.run(flags...)
	if err != nil {
		return fmt.Errorf("k8s cluster deployment failed: %w", err)
	}

	return nil
}

// Upgrade runs 'kubeone upgrade' command to upgrade the cluster
func (k1 *Kubeone) Upgrade(upgradeFlags []string) error {
	flags := []string{"upgrade",
		"--tfjson", "tf.json",
		"--upgrade-machine-deployments",
		"--manifest", k1.ConfigurationFilePath}
	if len(upgradeFlags) != 0 {
		flags = append(flags, upgradeFlags...)
	}

	err := k1.run(flags...)
	if err != nil {
		return fmt.Errorf("k8s cluster upgrade failed: %w", err)
	}

	return nil
}

// Kubeconfig runs 'kubeone kubeconfig' command to create and store kubeconfig file
func (k1 *Kubeone) Kubeconfig() ([]byte, error) {
	var kubeconfigBuf bytes.Buffer

	exe := k1.build("kubeconfig",
		"--tfjson", "tf.json",
		"--manifest", k1.ConfigurationFilePath)
	testutil.StdoutTo(&kubeconfigBuf)(exe)

	if err := exe.Run(); err != nil {
		return nil, fmt.Errorf("creating kubeconfig failed: %w", err)
	}

	rawKubeconfig := kubeconfigBuf.String()
	homePath := os.Getenv("HOME")
	kubeconfigPath := fmt.Sprintf("%s/.kube/config", homePath)

	err := testutil.CreateFile(kubeconfigPath, rawKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("saving kubeconfig for given path %s failed: %w", kubeconfigPath, err)
	}

	return []byte(rawKubeconfig), nil
}

// Reset runs 'kubeone reset' command to destroy worker nodes and unprovision the cluster
func (k1 *Kubeone) Reset() error {
	err := k1.run("reset",
		"-v",
		"--tfjson", "tf.json",
		"--destroy-workers",
		"--manifest", k1.ConfigurationFilePath)
	if err != nil {
		return fmt.Errorf("destroing workers failed: %w", err)
	}

	return nil
}

// storeTFJson saves tf.json in the temporary test directory
func (k1 *Kubeone) storeTFJson(tfJSON string) error {
	tfJSONPath := fmt.Sprintf("%s/tf.json", k1.Dir)

	err := testutil.CreateFile(tfJSONPath, tfJSON)
	if err != nil {
		return fmt.Errorf("saving tf.json for given path %s failed: %w", tfJSONPath, err)
	}

	return nil
}

func (k1 *Kubeone) build(args ...string) *testutil.Exec {
	return testutil.NewExec("kubeone",
		testutil.WithArgs(args...),
		testutil.WithEnv(os.Environ()),
		testutil.InDir(k1.Dir),
	)
}

func (k1 *Kubeone) run(args ...string) error {
	return k1.build(args...).Run()
}
