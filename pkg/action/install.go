package action

import (
	"fmt"
	c7nclient "github.com/choerodon/c7nctl/pkg/client"
	c7nconsts "github.com/choerodon/c7nctl/pkg/common/consts"
	"github.com/choerodon/c7nctl/pkg/common/graph"
	"github.com/choerodon/c7nctl/pkg/config"
	"github.com/choerodon/c7nctl/pkg/resource"
	c7nslaver "github.com/choerodon/c7nctl/pkg/slaver"
	c7nutils "github.com/choerodon/c7nctl/pkg/utils"
	std_errors "github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"os"
	"strings"
)

type Install struct {
	cfg *C7nConfiguration

	ResourceClient *resource.Client

	Name      string
	Namespace string
	// 安装资源的路径，helm 的 values.yaml 文件在其路径下的 values 文件夹中
	ResourcePath string
	Version      string
	HelmValues   string
	//
	C7nGatewayUrl string
	ClientOnly    bool

	// 以下都是初始化到 InstallDefinition 的配置项
	Prefix          string
	ImageRepository string
	ChartRepository string
	DatasourceTpl   string
	ThinMode        bool
}

func NewInstall(cfg *C7nConfiguration) *Install {
	return &Install{
		cfg: cfg,
	}
}

// 设置 install 的值
func (i *Install) Setup(c *config.C7nConfig) {
	log.Debug("Initialize config to Install")
	// 配置优先级 flag > config.yaml > 默认值
	// 当 i.Version 不存在时，设置成 c.Version 或者默认值
	if c.Version == "" {
		// TODO 在打包时根据 TAG 设置 version
		c.Version = c7nconsts.Version
	}
	if i.Version == "" {
		i.Version = c.Version
	}
	log.Debugf("Choerodon version is %s", i.Version)

	if c.Spec.ResourcePath == "" {
		c.Spec.ResourcePath = c7nconsts.OpenSourceResourceURL
		// 默认到 gitee 上获取资源文件
		if i.ResourceClient.Business {
			c.Spec.ResourcePath = c7nconsts.BusinessResourcePath
		}
	}
	if i.ResourcePath == "" {
		i.ResourcePath = c.Spec.ResourcePath
	}
	log.Debugf("Install file path is %s", i.ResourcePath)
	if i.HelmValues == "" {
		i.HelmValues = c7nconsts.DefaultHelmValuesPath
	}
	log.Debugf("Helm values dir is %s", i.HelmValues)

	// 配置优先级 flag > config.yaml > install.yml > 默认值
	// 通过 C7nConfig 将配置值传递到 InstallDefinition
	log.Debug("Initialize flag to C7nConfig")
	if i.ImageRepository != "" {
		c.Spec.ImageRepository = i.ImageRepository
	}
	log.Debugf("Image repository is %s", c.GetImageRepository())
	if i.ChartRepository != "" {
		c.Spec.ChartRepository = i.ChartRepository
	}
	log.Debugf("Chart repository is %s", c.GetImageRepository())

	if i.DatasourceTpl != "" {
		c.Spec.DatasourceTpl = i.DatasourceTpl
	}
	log.Debugf("Datasource template is %s", c.GetImageRepository())

	if i.Prefix != "" {
		c.Spec.Prefix = i.Prefix
	}
	log.Debugf("Prefix is %s", c.GetImageRepository())

	if i.ThinMode {
		c.Spec.ThinMode = true
	}
}

func (i *Install) Run(instDef *resource.InstallDefinition) (err error) {
	// 检查资源，并将现有集群的硬件信息保存到 metrics
	if i.ClientOnly || i.ThinMode {
		log.Info("Running Client only, So skip up check cluster resource")
	} /*else if err = i.cfg.CheckResource(&instDef.Spec.Resources); err != nil {
		return err
	}*/
	if err = i.CheckNamespace(); err != nil {
		return err
	}

	i.cfg.CreateImagePullSecret(instDef.Spec.Basic.DockerRegistry)
	// 初始化 slaver
	stopCh := make(chan struct{})
	if _, err = instDef.Spec.Basic.Slaver.InitSalver(i.cfg.KubeClient.GetClientSet(), i.Namespace, stopCh); err != nil {
		return std_errors.WithMessage(err, "Create Slaver failed")
	}
	defer func() {
		stopCh <- struct{}{}
	}()

	// 渲染 Release
	c7nclient.InitC7nLogs(i.cfg.KubeClient.GetClientSet(), i.Namespace)
	if err = instDef.RenderReleases(i.Name, i.cfg.KubeClient, i.Namespace); err != nil {
		return err
	}
	if i.ClientOnly {
		instDef.PrintRelease(i.Name)
	}

	// 安装 release
	if err = i.InstallReleases(instDef); err != nil {
		return err
	}

	// 清理历史的job
	// c.Clean()
	return nil
}

func (i *Install) InstallReleases(inst *resource.InstallDefinition) error {
	rs := inst.Spec.Release[i.Name]
	releaseGraph := graph.NewReleaseGraph(rs)
	installQueue := releaseGraph.TopoSortByKahn()

	for !installQueue.IsEmpty() {
		rls := installQueue.Dequeue()
		log.Infof("start installing release %s", rls.Name)
		// 获取的 values.yaml 必须经过渲染，只能放在 id 中
		if !strings.HasSuffix(i.ResourcePath, "/") {
			i.ResourcePath += "/"
		}

		rvurl := fmt.Sprintf("/%s/%s.yaml", c7nconsts.DefaultHelmValuesPath, rls.Name)
		rr, err := i.ResourceClient.GetResource(i.Version, rvurl)
		log.Debugf("Get redern values  %s", rr)
		if err != nil {
			return err
		}
		vals, err := inst.RenderHelmValues(rls, rr)
		if err != nil {
			return err
		}

		if rls.RepoURL == "" {
			rls.RepoURL = inst.Spec.Basic.ChartRepository
		}
		if rls.Version == "" {
			version, err := c7nutils.GetReleaseTag(rls.RepoURL, rls.Chart, i.Version)
			if err != nil {
				return err
			}
			rls.Version = version
		}
		args := c7nclient.ChartArgs{
			RepoUrl:     rls.RepoURL,
			Namespace:   i.Namespace,
			ReleaseName: inst.GetReleaseName(rls.Name),
			ChartName:   rls.Chart,
			Version:     rls.Version,
		}
		if rls.RepoURL != "" {
			args.RepoUrl = rls.RepoURL
		}

		if i.ClientOnly {
			fmt.Printf("------------- Installingg helm release %s -------------", rls.Name)
			fmt.Println("\nargs:")
			c7nutils.PrettyPrint(args)
			fmt.Println("\nhelm values:")
			c7nutils.PrettyPrint(vals)
			continue
		}
		if err = i.installRelease(rls, vals, args, &inst.Spec.Basic.Slaver); err != nil {
			return std_errors.WithMessage(err, fmt.Sprintf("Release %s install failed", rls.Name))
		}
	}
	return nil
}

func (i *Install) installRelease(rls *resource.Release, vals map[string]interface{}, args c7nclient.ChartArgs, slaver *c7nslaver.Slaver) error {
	task, err := c7nclient.GetTask(rls.Name)
	if err != nil {
		return err
	}
	if task.Status == c7nconsts.SucceedStatus {
		log.Infof("Release %s is already installed", rls.Name)
		return nil
	}

	// 等待依赖项安装完成
	for _, r := range rls.Requirements {
		i.cfg.CheckReleasePodRunning(r, i.Namespace)
	}

	// 执行前置命令
	if err := rls.ExecutePreCommands(slaver); err != nil {
		task.Status = c7nconsts.FailedStatus
		return std_errors.WithMessage(err, fmt.Sprintf("Release %s execute pre commands failed", rls.Name))
	}

	log.Infof("installing %s", rls.Name)
	// TODO 使用统一的 io.writer
	// 使用 upgrade --install cmd
	_, err = i.cfg.HelmClient.Upgrade(args, vals, os.Stdout)
	if err != nil {
		task.Status = c7nconsts.FailedStatus
		return err
	}
	// 将异步的 afterInstall 改为同步，AfterInstall 其依赖检查依靠前面的
	if err := rls.ExecuteAfterTasks(slaver); err != nil {
		task.Status = c7nconsts.FailedStatus
		return std_errors.WithMessage(err, "Execute after task failed")
	}

	task.Status = c7nconsts.SucceedStatus
	log.Infof("Successfully installed %s", rls.Name)

	// 完成后更新 task 状态
	defer c7nclient.SaveTask(*task)
	return err
}
