package resource

import (
	"fmt"
	c7nclient "github.com/choerodon/c7nctl/pkg/client"
	c7ncfg "github.com/choerodon/c7nctl/pkg/config"
	c7nctx "github.com/choerodon/c7nctl/pkg/context"
	c7nutils "github.com/choerodon/c7nctl/pkg/utils"
	log "github.com/sirupsen/logrus"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Persistence struct {
	CommonLabels map[string]string
	AccessModes  []v1.PersistentVolumeAccessMode
	Capacity     v1.ResourceList
	Name         string
	PvcEnabled   bool
	Path         string
	RootPath     string
	Size         string
	Namespace    string
	RefPvName    string
	RefPvcName   string
	Mode         string
	Own          string
	MountOptions []string
}

// check and create pv with defined pv schema
func (p *Persistence) CheckOrCreatePv(per *c7ncfg.Persistence) error {
	if p.RefPvName == "" {
		p.RefPvName = p.Name
	}
	ti, err := c7nctx.GetTaskFromCM(p.Namespace, p.Name)
	if err != nil {
		return err
	}
	if ti != nil && ti.Type == c7nctx.PvType {
		log.Infof("using exist pv [%s]", ti.RefName)
		p.RefPvName = ti.RefName
		return nil
	}
	// 当为NFS时可以忽略 PV，现在只支持 storage Class
	/*
		if context.Ctx.UserConfig.IgnorePv() {
			p.RefPvName = ""
			log.Debug("ignore create pv because specify storage class and no other persistence config")
			return nil
		}
	*/

	// 当 slaver 存在时，在它的 pvc 中创建 Persistence 挂载的目录？应该是在新建的 PVC 中创建目录
	/*
		dir := slaver.Dir{
			Mode: p.Mode,
			Path: p.Path,
			Own:  p.Own,
		}
		if context.Ctx.Slaver == nil {
			goto checkpv
		}

		if err := context.Ctx.Slaver.MakeDir(dir); dir.Path != "" && err != nil {
			return err
		}

	*/
	// 获得一个不重复的 pv name
	for {
		if got, _ := p.getPv(); got {
			p.RefPvName = fmt.Sprintf("%s-%s", p.Name, c7nutils.RandomString())
		} else {
			break
		}
	}
	return p.createPv(per.StorageClassName, per.GetPersistentVolumeSource(""))
}

func (p *Persistence) CheckOrCreatePvc(sc string) error {
	if p.RefPvcName == "" {
		p.RefPvcName = p.Name
	}
	ti, err := c7nctx.GetTaskFromCM(p.Namespace, p.Name)
	if err != nil {
		return err
	}
	if ti != nil && ti.Type == c7nctx.PvcType {
		p.RefPvcName = ti.RefName
		return nil
	}
	// 获得一个不重复的 pvc name
	for {
		if got, _ := p.getPvc(); got {
			p.RefPvcName = fmt.Sprintf("%s-%s", p.Name, c7nutils.RandomString())
		} else {
			break
		}
	}
	return p.createPvc(sc)
}

func (p *Persistence) createPv(sc string, pvs v1.PersistentVolumeSource) error {
	log.Infof("creating pv %s", p.RefPvName)
	if len(p.AccessModes) == 0 {
		p.AccessModes = []v1.PersistentVolumeAccessMode{"ReadWriteOnce"}
	}

	if p.Capacity == nil {
		p.Capacity = make(map[v1.ResourceName]resource.Quantity)
		q := resource.MustParse(p.Size)
		p.Capacity["storage"] = q
	}

	mountOptions := p.MountOptions

	storageClassName := sc

	pv := &v1.PersistentVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolume",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   p.RefPvName,
			Labels: p.CommonLabels,
		},
		Spec: v1.PersistentVolumeSpec{
			AccessModes:            p.AccessModes,
			Capacity:               p.Capacity,
			PersistentVolumeSource: pvs,
			MountOptions:           mountOptions,
			StorageClassName:       storageClassName,
		},
	}

	news := p.prepareTaskInfo()
	defer c7nctx.AddTaskToCM(p.Namespace, *news)

	_, err := c7nclient.CreatePv(pv)
	if err != nil {
		news.Status = c7nctx.FailedStatus
		news.Reason = err.Error()
		return err
	}
	log.Info("created pv [%s]", p.RefPvName)
	return nil
}

func (p *Persistence) createPvc(sc string) error {
	q := resource.MustParse(p.Size)

	resList := v1.ResourceList{
		"storage": q,
	}
	res := v1.ResourceRequirements{
		Requests: resList,
	}

	storageClassName := sc

	pvc := &v1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   p.RefPvcName,
			Labels: p.CommonLabels,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes:      p.AccessModes,
			Resources:        res,
			VolumeName:       p.RefPvName,
			StorageClassName: &storageClassName,
		},
	}

	ti := p.prepareTaskInfo()
	ti.Type = c7nctx.PvcType
	ti.RefName = p.RefPvcName
	defer c7nctx.AddTaskToCM(p.Namespace, *ti)

	_, err := c7nclient.CreatePvc(p.Namespace, pvc)
	if err != nil {
		log.Error(err)
		ti.Status = c7nctx.FailedStatus
		ti.Reason = err.Error()
		return err
	}
	log.Info("created pvc [%s]", p.RefPvcName)
	return nil
}

func (p *Persistence) prepareTaskInfo() *c7nctx.TaskInfo {
	ti := &c7nctx.TaskInfo{
		Name:      p.Name,
		Namespace: p.Namespace,
		Type:      c7nctx.PvType,
		Status:    c7nctx.SucceedStatus,
		RefName:   p.RefPvName,
	}
	return ti
}

// Get exist pv
func (p *Persistence) getPv() (hasFound bool, pv *v1.PersistentVolume) {
	pv, err := c7nclient.GetPv(p.RefPvName)
	if err != nil {
		if c7nctx.IsNotFound(err) {
			return false, pv
		}
	}
	return true, pv
}

// Get exist pvc
func (p *Persistence) getPvc() (hasFound bool, pvc *v1.PersistentVolumeClaim) {
	pvc, err := c7nclient.GetPvc(p.Namespace, p.RefPvName)

	if err != nil {
		if c7nctx.IsNotFound(err) {
			return false, pvc
		}
	}
	return true, pvc
}
