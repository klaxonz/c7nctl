package main

import (
	"fmt"
	"github.com/choerodon/c7nctl/pkg/action"
	"github.com/choerodon/c7nctl/pkg/config"
	c7nconsts "github.com/choerodon/c7nctl/pkg/consts"
	"github.com/choerodon/c7nctl/pkg/context"
	"github.com/choerodon/c7nctl/pkg/resource"
	c7n_utils "github.com/choerodon/c7nctl/pkg/utils"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"io"
)

const installC7nDesc = `One-click installation choerodon, When your prepared k8s, helm and NFS.
To install choerodon, you must set up the choerodon install configuration file
and specify the file with "--c7n-config <install-c7n-config.yaml>".

Ensure you run this within server can vista k8s.
`

func newInstallC7nCmd(cfg *action.C7nConfiguration, out io.Writer) *cobra.Command {
	c := action.NewChoerodon(cfg)

	cmd := &cobra.Command{
		Use:   "c7n",
		Short: "One-click installation choerodon",
		Long:  installC7nDesc,
		Run: func(_ *cobra.Command, args []string) {
			setUserConfig(c.SkipInput)
			if err := runInstallC7n(c); err != nil {
				log.Error(err) // errors.WithMessage(err, "Install Choerodon failed")
			}
			log.Info("Install Choerodon succeed")
		},
	}

	flags := cmd.PersistentFlags()
	addInstallFlags(flags, c)

	// set defaults from environment
	return cmd
}

func runInstallC7n(c *action.Choerodon) error {
	userConfig, err := action.GetUserConfig(settings.ConfigFile)

	if err != nil {
		return errors.WithMessage(err, "Failed to get user config file")
	}
	c.UserConfig = userConfig

	// 当 version 没有设置时，从 git repo 获取最新版本(本地的 config.yaml 也有配置 version ？)
	if c.Version == "" {
		c.Version = c7n_utils.GetVersion(c7nconsts.DefaultGitBranch)
	}
	// config.yaml 的 version 配置不会生效
	userConfig.Version = c.Version

	id, _ := c.GetInstallDef(settings.ResourceFile)

	// 初始化 helmInstall
	// 只有 id 中用到了 RepoUrl
	if id.Spec.Basic.RepoURL != "" {
		c.RepoUrl = id.Spec.Basic.RepoURL
	} else {
		c.RepoUrl = c7nconsts.DefaultRepoUrl
	}

	// 检查硬件资源
	if err := action.CheckResource(&id.Spec.Resources); err != nil {
		return err
	}
	if err := action.CheckNamespace(c.Namespace); err != nil {
		return err
	}

	stopCh := make(chan struct{})
	_, err = c.PrepareSlaver(stopCh)
	if err != nil {
		return errors.WithMessage(err, "Create Slaver failed")
	}

	defer func() {
		stopCh <- struct{}{}
	}()

	// 渲染 Release
	for _, rls := range id.Spec.Release {

		// 传入参数的是 *Release
		if err := id.RenderRelease(rls, userConfig); err != nil {
			return err
		}
		// 检测域名
		if err = c.CheckReleaseDomain(rls); err != nil {
			return errors.WithMessage(err, fmt.Sprintf("Release %s's domain is invalid", rls.Name))
		}
	}

	releaseGraph := resource.NewReleaseGraph(id.Spec.Release)
	installQueue := releaseGraph.TopoSortByKahn()

	for !installQueue.IsEmpty() {
		rls := installQueue.Dequeue()
		// TODO move to renderRelease
		rls.Namespace = c.Namespace
		rls.Prefix = c.Prefix

		// 获取的 values.yaml 必须经过渲染，只能放在 id 中
		vals, err := id.RenderHelmValues(rls, userConfig)
		if err != nil {
			return err
		}
		if err = c.InstallRelease(rls, vals); err != nil {
			return errors.WithMessage(err, fmt.Sprintf("Release %s install failed", rls.Name))
		}
	}
	// 等待所有 afterTask 执行完成。
	c.Wg.Wait()
	c.SendMetrics(err)
	// 清理历史的job，cm，slaver 等
	if err := action.CleanJobs(); err != nil {
		return err
	}

	return err
}

func addInstallFlags(fs *pflag.FlagSet, client *action.Choerodon) {
	// moved to EnvSettings
	//fs.StringVarP(&client.ResourceFile, "resource-file", "r", "", "Resource file to read from, It provide which app should be installed")
	//fs.StringVarP(&client.ConfigFile, "c7n-config", "c", "", "User Config file to read from, User define config by this file")
	//fs.StringVarP(&client.Namespace, "namespace", "n", "c7n-system", "set namespace which install choerodon")

	fs.StringVar(&client.Version, "version", "", "specify a version")
	fs.StringVar(&client.Prefix, "prefix", "", "add prefix to all helm release")

	fs.BoolVar(&client.NoTimeout, "no-timeout", false, "disable resource job timeout")
	fs.BoolVar(&client.SkipInput, "skip-input", false, "use default username and password to avoid user input")
}

func setUserConfig(skipInput bool) {
	// 在 c7nctl.initConfig() 中 viper 获取了默认的配置文件
	c := config.Cfg
	if !c.Terms.Accepted && !skipInput {
		c7n_utils.AskAgreeTerms()
		mail := inputUserMail()
		c.Terms.Accepted = true
		c.OpsMail = mail
		viper.Set("terms", c.Terms)
		viper.Set("opsMail", mail)
	} else {
		log.Info("your are execute job by skip input option, so we think you had allowed we collect your information")
	}
}

func inputUserMail() string {
	mail, err := c7n_utils.AcceptUserInput(context.Input{
		Password: false,
		Tip:      "请输入您的邮箱以便通知您重要的更新(Please enter your email address):  ",
		Regex:    "^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$",
	})
	c7n_utils.CheckErr(err)
	return mail
}