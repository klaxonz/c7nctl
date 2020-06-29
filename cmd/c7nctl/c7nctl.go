// Copyright © 2018 choerodon <EMAIL ADDRESS>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"github.com/choerodon/c7nctl/pkg/action"
	"github.com/choerodon/c7nctl/pkg/c7nclient"
	"github.com/choerodon/c7nctl/pkg/cli"
	"github.com/choerodon/c7nctl/pkg/client"
	"github.com/choerodon/c7nctl/pkg/config"
	"github.com/choerodon/c7nctl/pkg/consts"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	haction "helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/kube"

	"os"
)

var (
	clientPlatformConfig c7nclient.C7NConfig
	clientConfig         c7nclient.C7NContext

	settings = cli.New()
)

func main() {
	c7nCfg := new(action.C7nConfiguration)

	cmd := newRootCmd(c7nCfg, os.Stdout, os.Args[1:])
	cobra.OnInitialize(func() {
		// settings.namespace 是不能设置的，后面的 namespace 都以 settings.Namespace() 为准
		// c7nCfg.HelmInstall = client.NewHelmInstall(settings)
		initConfig()
		// 初始化 helm3Client
		cfg := initConfiguration(settings.Namespace)
		c7nCfg.HelmClient = client.NewHelm3Client(cfg)
		// 初始化 kubeClient
		c7nCfg.KubeClient, _ = client.GetKubeClient()
	})
	if err := cmd.Execute(); err != nil {
		log.Debug(err)
	}
	defer viper.WriteConfig()
}

// 初始化 config 与 c7n api 操作有关
// initConfig reads in config file and ENV variables if set.
func initConfig() {

	// set default configuration is $HOME/.c7n/config.yml
	viper.AddConfigPath(consts.DefaultConfigPath)
	viper.SetConfigName(consts.DefaultConfigFileName)
	viper.SetConfigType("yml")

	// read in environment variables that match
	viper.AutomaticEnv()

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found; Set default config to predefined path
			log.Warn(err)
			if err = viper.Unmarshal(&config.Cfg); err != nil {
				log.Error(err)
			}
			viper.WriteConfig()
		} else {
			// Config file was found but another error was produced
			log.Error(err)
			os.Exit(1)
		}
	}
	log.WithField("config", viper.ConfigFileUsed()).Info("using configuration file")
	if err := viper.Unmarshal(&config.Cfg); err != nil {
		log.Error(err)
	}

}

func initConfiguration(namespace string) *haction.Configuration {
	actionConfig := new(haction.Configuration)
	helmDriver := os.Getenv("HELM_DRIVER")
	// TODO 是否
	if err := actionConfig.Init(kube.GetConfig("", "", settings.Namespace), namespace, helmDriver, func(format string, v ...interface{}) {
		log.Warnf(format, v)
	}); err != nil {
		log.Fatal(err)
	}
	return actionConfig
}
