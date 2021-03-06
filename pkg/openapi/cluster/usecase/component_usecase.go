package usecase

import (
	"fmt"
	"strings"

	"github.com/goodrain/rainbond-operator/cmd/openapi/option"

	rainbondv1alpha1 "github.com/goodrain/rainbond-operator/pkg/apis/rainbond/v1alpha1"
	v1 "github.com/goodrain/rainbond-operator/pkg/openapi/types/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	plabels "k8s.io/apimachinery/pkg/labels"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("usecase_cluster")

type rbdComponentStatusFromSubObject func(cpn *rainbondv1alpha1.RbdComponent) (*v1.RbdComponentStatus, error)

// ComponentUseCase cluster componse case
type ComponentUseCase interface { // TODO: loop call
	Get(name string) (*v1.RbdComponentStatus, error)
	List(isInit bool) ([]*v1.RbdComponentStatus, error)
}

// ComponentUsecaseImpl cluster
type ComponentUsecaseImpl struct {
	cfg *option.Config
}

// NewComponentUsecase new componse case impl
func NewComponentUsecase(cfg *option.Config) *ComponentUsecaseImpl {
	return &ComponentUsecaseImpl{cfg: cfg}
}

// Get get
func (cc *ComponentUsecaseImpl) Get(name string) (*v1.RbdComponentStatus, error) {
	component, err := cc.cfg.RainbondKubeClient.RainbondV1alpha1().RbdComponents(cc.cfg.Namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return cc.typeRbdComponentStatus(component)
}

// List list
func (cc *ComponentUsecaseImpl) List(isInit bool) ([]*v1.RbdComponentStatus, error) {
	reqLogger := log.WithValues("Namespace", cc.cfg.Namespace)
	reqLogger.Info("Start listing RbdComponent associated controller")

	listOption := metav1.ListOptions{}
	if isInit {
		log.Info("get init component status list")
		listOption.LabelSelector = "priorityComponent=true"
	}
	components, err := cc.cfg.RainbondKubeClient.RainbondV1alpha1().RbdComponents(cc.cfg.Namespace).List(listOption)
	if err != nil {
		reqLogger.Error(err, "Listing RbdComponents")
		return nil, err
	}

	var statues []*v1.RbdComponentStatus
	for _, component := range components.Items {
		var status *v1.RbdComponentStatus
		if component.Name == "metrics-server" {
			// handle metrics-server service already case, rainbond cluster won't create metrics-server now
			if component.Annotations != nil && component.Annotations["v1beta1.metrics.k8s.io.exists"] == "true" {
				continue
			}
		}
		if component.Status == nil {
			// Initially, status may be nil
			status = &v1.RbdComponentStatus{
				Name:   component.Name,
				Status: v1.ComponentStatusIniting,
			}
		} else {
			status, err = cc.typeRbdComponentStatus(&component)
			if err != nil {
				reqLogger.Error(err, "Get RbdComponent status", "Name", component.Name)
				status = &v1.RbdComponentStatus{
					Name:            component.Name,
					Status:          v1.ComponentStatusFailed,
					Message:         "系统异常，请联系社区帮助",
					ISInitComponent: component.Spec.PriorityComponent,
					Reason:          fmt.Sprintf("get RbdComponent:%s status error: %s", component.Name, err.Error()),
				}
			}
		}
		statues = append(statues, status)
	}

	return statues, nil
}

func (cc *ComponentUsecaseImpl) typeRbdComponentStatus(cpn *rainbondv1alpha1.RbdComponent) (*v1.RbdComponentStatus, error) {
	reqLogger := log.WithValues("Namespace", cpn.Namespace, "Name", cpn.Name, "ControllerType", cpn.Status.ControllerType)
	reqLogger.Info("Start getting RbdComponent associated controller")

	k2fn := map[string]rbdComponentStatusFromSubObject{
		rainbondv1alpha1.ControllerTypeDeployment.String():  cc.rbdComponentStatusFromDeployment,
		rainbondv1alpha1.ControllerTypeStatefulSet.String(): cc.rbdComponentStatusFromStatefulSet,
		rainbondv1alpha1.ControllerTypeDaemonSet.String():   cc.rbdComponentStatusFromDaemonSet,
	}
	fn, ok := k2fn[cpn.Status.ControllerType.String()]
	if !ok {
		return nil, fmt.Errorf("unsupportted controller type: %s", cpn.Status.ControllerType.String())
	}

	status, err := fn(cpn)
	if err != nil {
		log.Error(err, "get RbdComponent associated controller")
		return nil, err
	}

	status.Status = v1.ComponentStatusCreating
	if status.Replicas == status.ReadyReplicas && status.Replicas > 0 {
		status.Status = v1.ComponentStatusRunning
	}

	for index, _ := range status.PodStatuses {
		if status.PodStatuses[index].Phase == "NotReady" {
			for _, container := range status.PodStatuses[index].ContainerStatuses {
				if container.State != "Running" {
					status.PodStatuses[index].Message = container.Message
					status.PodStatuses[index].Reason = container.Reason
					break
				}
			}
		}
	}

	return status, nil
}

func (cc *ComponentUsecaseImpl) rbdComponentStatusFromDeployment(cpn *rainbondv1alpha1.RbdComponent) (*v1.RbdComponentStatus, error) {
	reqLogger := log.WithValues("Namespace", cpn.Namespace, "Name", cpn.Name, "ControllerType", cpn.Status.ControllerType)
	reqLogger.Info("Start getting RbdComponent associated deployment")

	deploy, err := cc.cfg.KubeClient.AppsV1().Deployments(cpn.Namespace).Get(cpn.Status.ControllerName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	status := &v1.RbdComponentStatus{
		Name:            cpn.Name,
		Replicas:        deploy.Status.Replicas,
		ReadyReplicas:   deploy.Status.ReadyReplicas,
		ISInitComponent: cpn.Spec.PriorityComponent,
	}

	labels := deploy.Spec.Template.Labels
	podStatuses, err := cc.listPodStatues(deploy.Namespace, labels)
	if err != nil {
		reqLogger.Error(err, "List deployment associated pods", "labels", labels)
	}
	status.PodStatuses = podStatuses

	return status, nil
}

func (cc *ComponentUsecaseImpl) rbdComponentStatusFromStatefulSet(cpn *rainbondv1alpha1.RbdComponent) (*v1.RbdComponentStatus, error) {
	reqLogger := log.WithValues("Namespace", cpn.Namespace, "Name", cpn.Name, "ControllerType", cpn.Status.ControllerType)
	reqLogger.Info("Start getting RbdComponent associated statefulset")

	sts, err := cc.cfg.KubeClient.AppsV1().StatefulSets(cpn.Namespace).Get(cpn.Status.ControllerName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	status := &v1.RbdComponentStatus{
		Name:            cpn.Name,
		Replicas:        sts.Status.Replicas,
		ReadyReplicas:   sts.Status.ReadyReplicas,
		ISInitComponent: cpn.Spec.PriorityComponent,
	}
	labels := sts.Spec.Template.Labels
	podStatuses, err := cc.listPodStatues(sts.Namespace, labels)
	if err != nil {
		reqLogger.Error(err, "List deployment associated pods", "labels", labels)
	}
	status.PodStatuses = podStatuses

	return status, nil
}

func (cc *ComponentUsecaseImpl) rbdComponentStatusFromDaemonSet(cpn *rainbondv1alpha1.RbdComponent) (*v1.RbdComponentStatus, error) {
	reqLogger := log.WithValues("Namespace", cpn.Namespace, "Name", cpn.Name, "ControllerType", cpn.Status.ControllerType)
	reqLogger.Info("Start getting RbdComponent associated daemonset")

	ds, err := cc.cfg.KubeClient.AppsV1().DaemonSets(cpn.Namespace).Get(cpn.Status.ControllerName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	status := &v1.RbdComponentStatus{
		Name:            cpn.Name,
		Replicas:        ds.Status.DesiredNumberScheduled,
		ReadyReplicas:   ds.Status.NumberAvailable,
		ISInitComponent: cpn.Spec.PriorityComponent,
	}

	labels := ds.Spec.Template.Labels
	podStatuses, err := cc.listPodStatues(ds.Namespace, labels)
	if err != nil {
		reqLogger.Error(err, "List deployment associated pods", "labels", labels)
	}
	status.PodStatuses = podStatuses

	return status, nil
}

func (cc *ComponentUsecaseImpl) listPodStatues(namespace string, labels map[string]string) ([]v1.PodStatus, error) {
	selector := plabels.SelectorFromSet(labels)
	opts := metav1.ListOptions{
		LabelSelector: selector.String(),
	}
	podList, err := cc.cfg.KubeClient.CoreV1().Pods(namespace).List(opts)
	if err != nil {
		return nil, err
	}

	var podStatuses []v1.PodStatus
	for _, pod := range podList.Items {
		podStatus := v1.PodStatus{
			Name:    pod.Name,
			Phase:   "NotReady", // default phase NotReady, util PodReady condition is true
			HostIP:  pod.Status.HostIP,
			Reason:  pod.Status.Reason,
			Message: pod.Status.Message,
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == "True" {
				podStatus.Phase = "Ready"
				break
			}
		}
		var containerStatuses []v1.PodContainerStatus
		for _, cs := range pod.Status.ContainerStatuses {
			containerStatus := v1.PodContainerStatus{
				Image: cs.Image,
				Ready: cs.Ready,
			}
			if cs.ContainerID != "" {
				containerStatus.ContainerID = strings.Replace(cs.ContainerID, "docker://", "", -1)[0:8]
			}

			// TODO: move out
			if cs.State.Running != nil {
				containerStatus.State = "Running"
			}
			if cs.State.Waiting != nil {
				containerStatus.State = "Waiting"
				containerStatus.Reason = cs.State.Waiting.Reason
				containerStatus.Message = cs.State.Waiting.Message
			}
			if cs.State.Terminated != nil {
				containerStatus.State = "Terminated"
				containerStatus.Reason = cs.State.Terminated.Reason
				containerStatus.Message = cs.State.Terminated.Message
			}

			containerStatuses = append(containerStatuses, containerStatus)
		}
		podStatus.ContainerStatuses = containerStatuses
		podStatuses = append(podStatuses, podStatus)
	}

	return podStatuses, nil
}
