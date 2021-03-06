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

// Package config implements the KubeOne configuration API.
// Deprecated: This API is deprecated and is used only for migration purposes.
// The config package will be removed after 2019-08-12.
package config

import (
	"encoding/base64"
	"net"
	"os"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/pkg/errors"
)

// ENV variable names with credential in them that machine-controller expects to see
const (
	AWSAccessKeyID          = "AWS_ACCESS_KEY_ID"
	AWSSecretAccessKey      = "AWS_SECRET_ACCESS_KEY"
	DigitalOceanTokenKey    = "DO_TOKEN"
	GoogleServiceAccountKey = "GOOGLE_SERVICE_ACCOUNT"
	HetznerTokenKey         = "HZ_TOKEN"
	OpenStackAuthURL        = "OS_AUTH_URL"
	OpenStackDomainName     = "OS_DOMAIN_NAME"
	OpenStackPassword       = "OS_PASSWORD"
	OpenStackTenantName     = "OS_TENANT_NAME"
	OpenStackUserName       = "OS_USER_NAME"
	PacketAPIKey            = "PACKET_API_KEY"
	PacketProjectID         = "PACKET_PROJECT_ID"
	VSphereAddress          = "VSPHERE_ADDRESS"
	VSpherePasswords        = "VSPHERE_PASSWORD"
	VSphereUsername         = "VSPHERE_USERNAME"
)

// Cluster describes our entire configuration.
type Cluster struct {
	Name              string                  `json:"name"`
	Hosts             []*HostConfig           `json:"hosts"`
	APIServer         APIServerConfig         `json:"apiserver"`
	Provider          ProviderConfig          `json:"provider"`
	Versions          VersionConfig           `json:"versions"`
	Network           NetworkConfig           `json:"network"`
	Proxy             ProxyConfig             `json:"proxy"`
	Workers           []WorkerConfig          `json:"workers"`
	MachineController MachineControllerConfig `json:"machine_controller"`
	Features          Features                `json:"features"`
}

// DefaultAndValidate checks if the cluster config makes sense.
func (m *Cluster) DefaultAndValidate() error {
	if err := m.Provider.Validate(); err != nil {
		return errors.Wrap(err, "provider configuration is invalid")
	}

	if len(m.Hosts) == 0 {
		return errors.New("no master hosts specified")
	}

	m.Hosts[0].IsLeader = true

	for idx, host := range m.Hosts {
		// define a unique ID for each host
		m.Hosts[idx].ID = idx

		if err := host.AddDefaultsAndValidate(); err != nil {
			return errors.WithMessagef(err, "host %d is invalid", idx+1)
		}
	}

	if err := m.MachineController.DefaultAndValidate(m.Provider.Name); err != nil {
		return errors.Wrap(err, "failed to configure machine-controller")
	}

	if *m.MachineController.Deploy {
		for idx, workerset := range m.Workers {
			if err := workerset.Validate(); err != nil {
				return errors.WithMessagef(err, "worker set %d is invalid", idx+1)
			}
		}
	} else if len(m.Workers) > 0 {
		return errors.New("machine-controller deployment is disabled, but configuration still contains worker definitions")
	}

	if err := m.Network.Validate(); err != nil {
		return errors.Wrap(err, "network configuration is invalid")
	}

	if err := m.Features.Validate(); err != nil {
		return errors.Wrap(err, "oidc confiuration is invalid")
	}

	if m.APIServer.Address == "" {
		m.APIServer.Address = m.Hosts[0].PublicAddress
	}

	return nil
}

// Leader returns the first configured host. Only call this after
// validating the cluster config to ensure a leader exists.
func (m *Cluster) Leader() (*HostConfig, error) {
	for i := range m.Hosts {
		if m.Hosts[i].IsLeader {
			return m.Hosts[i], nil
		}
	}
	return nil, errors.New("leader not found")
}

// Followers returns all but the first configured host. Only call
// this after validating the cluster config to ensure hosts exist.
func (m *Cluster) Followers() []*HostConfig {
	return m.Hosts[1:]
}

// HostConfig describes a single master node.
type HostConfig struct {
	ID                int    `json:"-"`
	PublicAddress     string `json:"public_address"`
	PrivateAddress    string `json:"private_address"`
	SSHPort           int    `json:"ssh_port"`
	SSHUsername       string `json:"ssh_username"`
	SSHPrivateKeyFile string `json:"ssh_private_key_file"`
	SSHAgentSocket    string `json:"ssh_agent_socket"`

	// runtime information
	Hostname        string `json:"-"`
	OperatingSystem string `json:"-"`
	IsLeader        bool   `json:"-"`
}

func (m *HostConfig) addDefaults() {
	if len(m.PublicAddress) == 0 && len(m.PrivateAddress) > 0 {
		m.PublicAddress = m.PrivateAddress
	}
	if len(m.PrivateAddress) == 0 && len(m.PublicAddress) > 0 {
		m.PrivateAddress = m.PublicAddress
	}
	if len(m.SSHPrivateKeyFile) == 0 && len(m.SSHAgentSocket) == 0 {
		m.SSHAgentSocket = "env:SSH_AUTH_SOCK"
	}
	if m.SSHUsername == "" {
		m.SSHUsername = "root"
	}
}

// AddDefaultsAndValidate checks if the Config makes sense.
func (m *HostConfig) AddDefaultsAndValidate() error {
	m.addDefaults()

	if len(m.PublicAddress) == 0 {
		return errors.New("no public IP/address given")
	}

	if len(m.PrivateAddress) == 0 {
		return errors.New("no private IP/address given")
	}

	if len(m.SSHPrivateKeyFile) == 0 && len(m.SSHAgentSocket) == 0 {
		return errors.New("neither SSH private key nor agent socket given, don't know how to authenticate")
	}

	if len(m.SSHUsername) == 0 {
		return errors.New("no SSH username given")
	}

	return nil
}

// APIServerConfig describes the load balancer address.
type APIServerConfig struct {
	Address string `json:"address"`
}

// ProxyConfig object
type ProxyConfig struct {
	HTTPProxy  string `json:"http_proxy"`
	HTTPSProxy string `json:"https_proxy"`
	NoProxy    string `json:"no_proxy"`
}

// ProviderName represents the name of an provider
type ProviderName string

// ProviderName values
const (
	ProviderNameAWS          ProviderName = "aws"
	ProviderNameOpenStack    ProviderName = "openstack"
	ProviderNameHetzner      ProviderName = "hetzner"
	ProviderNameDigitalOcean ProviderName = "digitalocean"
	ProviderNameVSphere      ProviderName = "vsphere"
	ProviderNameGCE          ProviderName = "gce"
	ProviderNamePacket       ProviderName = "packet"
	ProviderNameNone         ProviderName = "none"
)

// ProviderConfig describes the cloud provider that is running the machines.
type ProviderConfig struct {
	Name        ProviderName `json:"name"`
	External    bool         `json:"external"`
	CloudConfig string       `json:"cloud_config"`
}

// Validate checks the ProviderConfig for errors
func (p *ProviderConfig) Validate() error {
	switch p.Name {
	case ProviderNameAWS:
	case ProviderNameOpenStack:
		if p.CloudConfig == "" {
			return errors.New("`provider.cloud_config` is required for openstack provider")
		}
	case ProviderNameHetzner:
	case ProviderNameDigitalOcean:
	case ProviderNameVSphere:
	case ProviderNameGCE:
	case ProviderNamePacket:
	case ProviderNameNone:
	default:
		return errors.Errorf("unknown provider name %q", p.Name)
	}

	return nil
}

// CloudProviderInTree detects is there in-tree cloud provider implementation for specified provider.
// List of in-tree provider can be found here: https://github.com/kubernetes/kubernetes/tree/master/pkg/cloudprovider
func (p *ProviderConfig) CloudProviderInTree() bool {
	switch p.Name {
	case ProviderNameAWS, ProviderNameGCE, ProviderNameOpenStack, ProviderNameVSphere:
		return true
	default:
		return false
	}
}

// VersionConfig describes the versions of Kubernetes that is installed.
type VersionConfig struct {
	Kubernetes string `json:"kubernetes"`
}

// Validate semversion of config
func (m *VersionConfig) Validate() error {
	v, err := semver.NewVersion(m.Kubernetes)
	if err != nil {
		return errors.Wrap(err, "unable to parse version string")
	}
	if v.Major() != 1 || v.Minor() < 13 {
		return errors.New("kubernetes versions lower than 1.13 are not supported")
	}
	return nil
}

// KubernetesCNIVersion returns kubernetes-cni package version
func (m *VersionConfig) KubernetesCNIVersion() string {
	s := semver.MustParse(m.Kubernetes)
	c, _ := semver.NewConstraint(">= 1.13.0, <= 1.13.4")

	switch {
	// Validation ensures that the oldest cluster version is 1.13.0.
	// Versions 1.13.0-1.13.4 uses 0.6.0, so it's safe to return 0.6.0
	// if >= 1.13.0, <= 1.13.4 constraint check successes.
	case c.Check(s):
		return "0.6.0"
	default:
		return "0.7.5"
	}
}

// NetworkConfig describes the node network.
type NetworkConfig struct {
	PodSubnetVal     string `json:"pod_subnet"`
	ServiceSubnetVal string `json:"service_subnet"`
	NodePortRangeVal string `json:"node_port_range"`
}

// PodSubnet returns the pod subnet or the default value.
func (m *NetworkConfig) PodSubnet() string {
	if m.PodSubnetVal != "" {
		return m.PodSubnetVal
	}

	return "10.244.0.0/16"
}

// ServiceSubnet returns the service subnet or the default value.
func (m *NetworkConfig) ServiceSubnet() string {
	if m.ServiceSubnetVal != "" {
		return m.ServiceSubnetVal
	}

	return "10.96.0.0/12"
}

// NodePortRange returns the node port range or the default value.
func (m *NetworkConfig) NodePortRange() string {
	if m.NodePortRangeVal != "" {
		return m.NodePortRangeVal
	}

	return "30000-32767"
}

// Validate checks the NetworkConfig for errors
func (m *NetworkConfig) Validate() error {
	if m.PodSubnetVal != "" {
		if _, _, err := net.ParseCIDR(m.PodSubnetVal); err != nil {
			return errors.Wrap(err, "invalid pod subnet specified")
		}
	}

	if m.ServiceSubnetVal != "" {
		if _, _, err := net.ParseCIDR(m.ServiceSubnetVal); err != nil {
			return errors.Wrap(err, "invalid service subnet specified")
		}
	}

	return nil
}

type providerConfig struct {
	CloudProviderSpec   map[string]interface{} `json:"cloudProviderSpec"`
	Labels              map[string]string      `json:"labels"`
	SSHPublicKeys       []string               `json:"sshPublicKeys"`
	OperatingSystem     string                 `json:"operatingSystem"`
	OperatingSystemSpec map[string]interface{} `json:"operatingSystemSpec"`
}

// WorkerConfig describes a set of worker machines.
type WorkerConfig struct {
	Name     string         `json:"name"`
	Replicas *int           `json:"replicas"`
	Config   providerConfig `json:"config"`
}

// Validate checks if the Config makes sense.
func (m *WorkerConfig) Validate() error {
	if m.Name == "" {
		return errors.New("no name given")
	}

	if m.Replicas == nil || *m.Replicas < 1 {
		return errors.New("replicas must be specified and >= 1")
	}

	return nil
}

// Features switches
type Features struct {
	PodSecurityPolicy PodSecurityPolicy `json:"pod_security_policy"`
	DynamicAuditLog   DynamicAuditLog   `json:"dynamic_audit_log"`
	MetricsServer     MetricsServer     `json:"metrics_server"`
	OpenIDConnect     OpenIDConnect     `json:"openid_connect"`
}

// PodSecurityPolicy feature flag
type PodSecurityPolicy struct {
	Enable *bool `json:"enable,omitempty"`
}

// DynamicAuditLog feature flag
type DynamicAuditLog struct {
	Enable *bool `json:"enable,omitempty"`
}

// MetricsServer feature flag
type MetricsServer struct {
	Enable *bool `json:"enable,omitempty"`
}

// OpenIDConnect feature flag
type OpenIDConnect struct {
	Enable bool                `json:"enable"`
	Config OpenIDConnectConfig `json:"config"`
}

// OpenIDConnectConfig config
type OpenIDConnectConfig struct {
	IssuerURL      string `json:"issuer_url"`
	ClientID       string `json:"client_id"`
	UsernameClaim  string `json:"username_claim"`
	UsernamePrefix string `json:"username_prefix"`
	GroupsClaim    string `json:"groups_claim"`
	GroupsPrefix   string `json:"groups_prefix"`
	RequiredClaim  string `json:"required_claim"`
	SigningAlgs    string `json:"signing_algs"`
	CAFile         string `json:"ca_file"`
}

// Validate features config
func (f *Features) Validate() error {
	if f.MetricsServer.Enable == nil {
		f.MetricsServer.Enable = boolPtr(true)
	}

	// Currently only validate OIDC config
	if !f.OpenIDConnect.Enable {
		return nil
	}

	if f.OpenIDConnect.Config.IssuerURL == "" {
		return errors.New("openid_connect.config.issuer_url can't be empty")
	}

	if f.OpenIDConnect.Config.ClientID == "" {
		return errors.New("openid_connect.config.client_id can't be empty")
	}

	return nil
}

// MachineControllerConfig controls
type MachineControllerConfig struct {
	Deploy *bool `json:"deploy"`
	// Provider is provider to be used for machine-controller
	// Defaults and must be same as chosen cloud provider, unless cloud provider is set to None
	Provider    ProviderName      `json:"provider"`
	Credentials map[string]string `json:"credentials"`
}

// DefaultAndValidate checks if the machine-controller config makes sense.
func (m *MachineControllerConfig) DefaultAndValidate(cloudProvider ProviderName) error {
	if m.Deploy == nil {
		m.Deploy = boolPtr(true)
	}
	if *m.Deploy == false {
		return nil
	}

	// If ProviderName is not None default to cloud provider and ensure user have not
	// manually provided machine-controller provider different than cloud provider.
	// If ProviderName is None, take user input or default to None.
	if cloudProvider != ProviderNameNone {
		if m.Provider == "" {
			m.Provider = cloudProvider
		}
		if m.Provider != cloudProvider {
			return errors.New("cloud provider must be same as machine-controller provider")
		}
	} else if cloudProvider == ProviderNameNone && m.Provider == "" {
		return errors.New("machine-controller deployed but no provider selected")
	}

	var err error
	m.Credentials, err = m.Provider.ProviderCredentials()
	if err != nil {
		return errors.Wrap(err, "failed to apply cloud provider credentials")
	}

	return nil
}

// ProviderEnvironmentVariable is used to match environment variable used by KubeOne to environment variable used by
// machine-controller.
type ProviderEnvironmentVariable struct {
	Name                  string
	MachineControllerName string
}

// ProviderCredentials match the cloudprovider and parses its credentials from
// environment
func (p ProviderName) ProviderCredentials() (map[string]string, error) {
	switch p {
	case ProviderNameAWS:
		creds := make(map[string]string)
		envCredsProvider := credentials.NewEnvCredentials()
		envCreds, err := envCredsProvider.Get()
		if err != nil {
			return nil, err
		}
		if envCreds.AccessKeyID != "" && envCreds.SecretAccessKey != "" {
			creds[AWSAccessKeyID] = envCreds.AccessKeyID
			creds[AWSSecretAccessKey] = envCreds.SecretAccessKey
			return creds, nil
		}

		// If env fails resort to config file
		configCredsProvider := credentials.NewSharedCredentials("", "")
		configCreds, err := configCredsProvider.Get()
		if err != nil {
			return nil, err
		}
		if configCreds.AccessKeyID != "" && configCreds.SecretAccessKey != "" {
			creds[AWSAccessKeyID] = configCreds.AccessKeyID
			creds[AWSSecretAccessKey] = configCreds.SecretAccessKey
			return creds, nil
		}

		return nil, errors.New("error parsing aws credentials")
	case ProviderNameOpenStack:
		return parseCredentialVariables([]ProviderEnvironmentVariable{
			{Name: OpenStackAuthURL},
			{Name: "OS_USERNAME", MachineControllerName: OpenStackUserName},
			{Name: OpenStackPassword},
			{Name: OpenStackDomainName},
			{Name: OpenStackTenantName},
		})
	case ProviderNameHetzner:
		return parseCredentialVariables([]ProviderEnvironmentVariable{
			{Name: "HCLOUD_TOKEN", MachineControllerName: HetznerTokenKey},
		})
	case ProviderNameDigitalOcean:
		return parseCredentialVariables([]ProviderEnvironmentVariable{
			{Name: "DIGITALOCEAN_TOKEN", MachineControllerName: DigitalOceanTokenKey},
		})
	case ProviderNameGCE:
		gsa, err := parseCredentialVariables([]ProviderEnvironmentVariable{
			{Name: "GOOGLE_CREDENTIALS", MachineControllerName: GoogleServiceAccountKey},
		})
		if err != nil {
			return nil, errors.WithStack(err)
		}
		// encode it before sending to secret to be consumed by
		// machine-controller, as machine-controller assumes it will be double encoded
		gsa[GoogleServiceAccountKey] = base64.StdEncoding.EncodeToString([]byte(gsa[GoogleServiceAccountKey]))
		return gsa, nil
	case ProviderNamePacket:
		return parseCredentialVariables([]ProviderEnvironmentVariable{
			{Name: "PACKET_AUTH_TOKEN", MachineControllerName: PacketAPIKey},
			{Name: PacketProjectID},
		})
	case ProviderNameVSphere:
		return parseCredentialVariables([]ProviderEnvironmentVariable{
			{Name: VSphereAddress},
			{Name: VSphereUsername},
			{Name: VSpherePasswords},
		})
	}

	return nil, errors.New("no provider matched")
}

func parseCredentialVariables(envVars []ProviderEnvironmentVariable) (map[string]string, error) {
	creds := make(map[string]string)
	for _, env := range envVars {
		if len(env.MachineControllerName) == 0 {
			env.MachineControllerName = env.Name
		}
		creds[env.MachineControllerName] = strings.TrimSpace(os.Getenv(env.Name))
		if creds[env.MachineControllerName] == "" {
			return nil, errors.Errorf("environment variable %s is not set, but is required", env.Name)
		}
	}
	return creds, nil
}

func boolPtr(val bool) *bool {
	return &val
}
