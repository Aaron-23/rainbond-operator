package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/goodrain/rainbond-operator/pkg/util/commonutil"
	rbdutil "github.com/goodrain/rainbond-operator/pkg/util/rbduitl"

	rainbondv1alpha1 "github.com/goodrain/rainbond-operator/pkg/apis/rainbond/v1alpha1"
	"github.com/goodrain/rainbond-operator/pkg/util/constants"
	"github.com/goodrain/rainbond-operator/pkg/util/k8sutil"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ChaosName = "rbd-chaos"

type chaos struct {
	ctx        context.Context
	client     client.Client
	component  *rainbondv1alpha1.RbdComponent
	cluster    *rainbondv1alpha1.RainbondCluster
	pkg        *rainbondv1alpha1.RainbondPackage
	labels     map[string]string
	db         *rainbondv1alpha1.Database
	etcdSecret *corev1.Secret
}

func NewChaos(ctx context.Context, client client.Client, component *rainbondv1alpha1.RbdComponent, cluster *rainbondv1alpha1.RainbondCluster, pkg *rainbondv1alpha1.RainbondPackage) ComponentHandler {
	return &chaos{
		ctx:       ctx,
		client:    client,
		component: component,
		cluster:   cluster,
		labels:    component.GetLabels(),
		pkg:       pkg,
	}
}

func (c *chaos) Before() error {
	db, err := getDefaultDBInfo(c.ctx, c.client, c.cluster.Spec.RegionDatabase, c.component.Namespace, DBName)
	if err != nil {
		return fmt.Errorf("get db info: %v", err)
	}
	c.db = db

	secret, err := etcdSecret(c.ctx, c.client, c.cluster)
	if err != nil {
		return fmt.Errorf("failed to get etcd secret: %v", err)
	}
	c.etcdSecret = secret

	return nil
}

func (c *chaos) Resources() []interface{} {
	return []interface{}{
		c.daemonSetForChaos(),
	}
}

func (c *chaos) After() error {
	return nil
}

func (c *chaos) daemonSetForChaos() interface{} {
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "grdata",
			MountPath: "/grdata",
		},
		{
			Name:      "dockersock",
			MountPath: "/var/run/docker.sock",
		}, {
			Name:      "cache",
			MountPath: "/cache",
		},
	}
	volumes := []corev1.Volume{
		{
			Name: "grdata",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: constants.GrDataPVC,
				},
			},
		},
		{
			Name: "dockersock",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/var/run/docker.sock",
					Type: k8sutil.HostPath(corev1.HostPathFile),
				},
			},
		},
		{
			Name: "cache",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: constants.CachePVC,
				},
			},
		},
	}
	args := []string{
		"--hostIP=$(POD_IP)",
		fmt.Sprintf("--log-level=%s", c.component.LogLevel()),
		c.db.RegionDataSource(),
		"--etcd-endpoints=" + strings.Join(etcdEndpoints(c.cluster), ","),
	}

	if c.etcdSecret != nil {
		volume, mount := volumeByEtcd(c.etcdSecret)
		volumeMounts = append(volumeMounts, mount)
		volumes = append(volumes, volume)
		args = append(args, etcdSSLArgs()...)
	}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ChaosName,
			Namespace: c.component.Namespace,
			Labels:    c.labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: c.labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:   ChaosName,
					Labels: c.labels,
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: commonutil.Int64(0),
					NodeSelector:                  c.cluster.Status.FirstMasterNodeLabel(),
					Tolerations: []corev1.Toleration{
						{
							Key:    c.cluster.Status.MasterRoleLabel,
							Effect: corev1.TaintEffectNoSchedule,
						},
					},
					ServiceAccountName: "rainbond-operator",
					HostAliases: []corev1.HostAlias{
						{
							IP:        c.cluster.GatewayIngressIP(),
							Hostnames: []string{rbdutil.GetImageRepository(c.cluster)},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            ChaosName,
							Image:           c.component.Spec.Image,
							ImagePullPolicy: c.component.ImagePullPolicy(),
							Env: []corev1.EnvVar{
								{
									Name: "POD_IP",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.podIP",
										},
									},
								},
								{
									Name:  "SOURCE_DIR",
									Value: "/cache/source",
								},
								{
									Name:  "CACHE_DIR",
									Value: "/cache",
								},
							},
							Args:         args,
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	return ds
}
