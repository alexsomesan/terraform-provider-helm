package helm

import (
	"bytes"
	"log"
	"sync"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/mitchellh/go-homedir"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	apimachineryschema "k8s.io/apimachinery/pkg/runtime/schema"
	memcached "k8s.io/client-go/discovery/cached/memory"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// KubeConfig is a RESTClientGetter interface implementation
type KubeConfig struct {
	ConfigData   *schema.ResourceData
	Namespace    *string
	clientConfig clientcmd.ClientConfig
	lock         sync.Mutex
}

// ToRESTConfig implemented interface method
func (k *KubeConfig) ToRESTConfig() (*rest.Config, error) {
	return k.ToRawKubeConfigLoader().ClientConfig()
}

// ToDiscoveryClient implemented interface method
func (k *KubeConfig) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	config, err := k.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	// The more groups you have, the more discovery requests you need to make.
	// given 25 groups (our groups + a few custom resources) with one-ish version each, discovery needs to make 50 requests
	// double it just so we don't end up here again for a while.  This config is only used for discovery.
	config.Burst = 100

	return memcached.NewMemCacheClient(discovery.NewDiscoveryClientForConfigOrDie(config)), nil
}

// ToRESTMapper implemented interface method
func (k *KubeConfig) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := k.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	expander := restmapper.NewShortcutExpander(mapper, discoveryClient)
	return expander, nil
}

// ToRawKubeConfigLoader implemented interface method
func (k *KubeConfig) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	k.lock.Lock()
	defer k.lock.Unlock()

	// Always persist config
	if k.clientConfig == nil {
		k.clientConfig = k.toRawKubeConfigLoader()
	}

	return k.clientConfig
}

func (k *KubeConfig) toRawKubeConfigLoader() clientcmd.ClientConfig {
	overrides := &clientcmd.ConfigOverrides{}
	loader := &clientcmd.ClientConfigLoadingRules{}

	if k8sGet(k.ConfigData, "load_config_file").(bool) {
		if configPath, ok := k8sGetOk(k.ConfigData, "config_path"); ok && configPath.(string) != "" {
			path, err := homedir.Expand(configPath.(string))
			if err != nil {
				return nil
			}
			loader.ExplicitPath = path

			ctx, ctxOk := k8sGetOk(k.ConfigData, "config_context")
			authInfo, authInfoOk := k8sGetOk(k.ConfigData, "config_context_auth_info")
			cluster, clusterOk := k8sGetOk(k.ConfigData, "config_context_cluster")
			if ctxOk || authInfoOk || clusterOk {
				if ctxOk {
					overrides.CurrentContext = ctx.(string)
					log.Printf("[DEBUG] Using custom current context: %q", overrides.CurrentContext)
				}

				overrides.Context = clientcmdapi.Context{}
				if authInfoOk {
					overrides.Context.AuthInfo = authInfo.(string)
				}
				if clusterOk {
					overrides.Context.Cluster = cluster.(string)
				}
				log.Printf("[DEBUG] Using overidden context: %#v", overrides.Context)
			}
		}
	}

	// Overriding with static configuration
	if v, ok := k8sGetOk(k.ConfigData, "insecure"); ok {
		overrides.ClusterInfo.InsecureSkipTLSVerify = v.(bool)
	}
	if v, ok := k8sGetOk(k.ConfigData, "cluster_ca_certificate"); ok {
		overrides.ClusterInfo.CertificateAuthorityData = bytes.NewBufferString(v.(string)).Bytes()
	}
	if v, ok := k8sGetOk(k.ConfigData, "client_certificate"); ok {
		overrides.AuthInfo.ClientCertificateData = bytes.NewBufferString(v.(string)).Bytes()
	}
	if v, ok := k8sGetOk(k.ConfigData, "host"); ok {
		// Server has to be the complete address of the kubernetes cluster (scheme://hostname:port), not just the hostname,
		// because `overrides` are processed too late to be taken into account by `defaultServerUrlFor()`.
		// This basically replicates what defaultServerUrlFor() does with config but for overrides,
		// see https://github.com/kubernetes/client-go/blob/v12.0.0/rest/url_utils.go#L85-L87
		hasCA := len(overrides.ClusterInfo.CertificateAuthorityData) != 0
		hasCert := len(overrides.AuthInfo.ClientCertificateData) != 0
		defaultTLS := hasCA || hasCert || overrides.ClusterInfo.InsecureSkipTLSVerify
		host, _, err := rest.DefaultServerURL(v.(string), "", apimachineryschema.GroupVersion{}, defaultTLS)
		if err != nil {
			return nil
		}

		overrides.ClusterInfo.Server = host.String()
	}
	if v, ok := k8sGetOk(k.ConfigData, "username"); ok {
		overrides.AuthInfo.Username = v.(string)
	}
	if v, ok := k8sGetOk(k.ConfigData, "password"); ok {
		overrides.AuthInfo.Password = v.(string)
	}
	if v, ok := k8sGetOk(k.ConfigData, "client_key"); ok {
		overrides.AuthInfo.ClientKeyData = bytes.NewBufferString(v.(string)).Bytes()
	}
	if v, ok := k8sGetOk(k.ConfigData, "token"); ok {
		overrides.AuthInfo.Token = v.(string)
	}

	if v, ok := k8sGetOk(k.ConfigData, "exec"); ok {
		exec := &clientcmdapi.ExecConfig{}
		if spec, ok := v.([]interface{})[0].(map[string]interface{}); ok {
			exec.APIVersion = spec["api_version"].(string)
			exec.Command = spec["command"].(string)
			exec.Args = expandStringSlice(spec["args"].([]interface{}))
			for kk, vv := range spec["env"].(map[string]interface{}) {
				exec.Env = append(exec.Env, clientcmdapi.ExecEnvVar{Name: kk, Value: vv.(string)})
			}
		} else {
			log.Printf("[ERROR] Failed to parse exec")
			return nil
		}
		overrides.AuthInfo.Exec = exec
	}

	overrides.Context.Namespace = "default"

	if k.Namespace != nil {
		overrides.Context.Namespace = *k.Namespace
	}

	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, overrides)

	if cfg == nil {
		log.Printf("[ERROR] Failed to initialize kubernetes config")
		return nil
	}

	log.Printf("[INFO] Successfully initialized config")

	return cfg
}
