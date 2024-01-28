package main

import (
	"context"
	"encoding/json"
	"fmt"
	slog "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/resource"
	"net/http"
	"strconv"

	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	PodAnnotation = "app.traderepublic.com/set-resources"
)

type podWebhook struct {
	Client     client.Client
	decoder    *admission.Decoder
	Annotation bool
}

func (a *podWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	slog.SetFormatter(&slog.JSONFormatter{})
	pod := &core.Pod{}
	if err := a.decoder.Decode(req, pod); err != nil {
		slog.WithFields(slog.Fields{"Event": "AdmissionError", "Source": "Webhook"}).Error(fmt.Sprintf("Error bad request while decoding pod info %s", err.Error()))
		return admission.Errored(http.StatusBadRequest, err)
	}

	// check for the existence of a pod annotation if enabled
	if a.Annotation {
		value, ok := pod.Annotations[PodAnnotation]
		if !ok {
			slog.WithFields(slog.Fields{"Event": "AdmissionAllowed", "Source": "Webhook"}).Info(fmt.Sprintf("No pod annotation found %s for pod %s in namespace %s", PodAnnotation, pod.Name, pod.Namespace))
			return admission.Allowed(fmt.Sprintf("Got no pod annotation. For setting resources, ignoring pod %s in namespace %s", pod.Name, pod.Namespace))
		}

		parsed, err := strconv.ParseBool(value)
		if err != nil {
			slog.WithFields(slog.Fields{"Event": "AdmissionError", "Source": "Webhook"}).Error(fmt.Sprintf("Cannot parse pod annotation %s as bool for pod %s in namespace %s %s", PodAnnotation, pod.Name, pod.Namespace, err.Error()))
			return admission.Errored(http.StatusBadRequest, err)
		}

		if !parsed {
			slog.WithFields(slog.Fields{"Event": "AdmissionAllowed", "Source": "Webhook"}).Info(fmt.Sprintf("Pod annotation found but set to false %s for pod %s in namespace %s", PodAnnotation, pod.Name, pod.Namespace))
			return admission.Allowed(fmt.Sprintf("Pod annotation present. But value is set as false for pod %s on namespace %s. Ignoring", pod.Name, pod.Namespace))
		}
	}

	if len(pod.OwnerReferences) == 0 {
		slog.WithFields(slog.Fields{"Event": "PodOwnerReferences", "Source": "Webhook"}).Info(fmt.Sprintf("Pod owner reference not found for pod %s in namespace %s. Pod is orphaned or has no owners.", pod.Name, pod.Namespace))
	}

	var ownerName, controllerName string

	switch pod.OwnerReferences[0].Kind {
	case "ReplicaSet":
		ownerName = pod.OwnerReferences[0].Name
		controllerName = GetMainControllerName(ownerName)
	case "DaemonSet", "StatefulSet":
		ownerName = pod.OwnerReferences[0].Name
		controllerName = pod.OwnerReferences[0].Name
	default:
		ownerName = pod.OwnerReferences[0].Name
		controllerName = GetMainControllerName(ownerName)
	}

	slog.WithFields(slog.Fields{"Event": "AdmissionAllowed", "Source": "Webhook"}).Info(fmt.Sprintf("Pod annotation found and set to True. Initiating resizer for pod under owner reference %s in namespace %s", controllerName, pod.Namespace))

	promInstance := PromConfig{}

	resourceValues := promInstance.Propagate(controllerName)

	for cont := range pod.Spec.Containers {
		container := &pod.Spec.Containers[cont]
		containerAvgCpu := container.Resources.Requests.Cpu().String()
		containerAvgMem := container.Resources.Requests.Memory().String()
		containerPeakCpu := container.Resources.Limits.Cpu().String()
		containerPeakMem := container.Resources.Limits.Memory().String()

		if resourceValues.AvgCpu != nil {
			for key, value := range resourceValues.AvgCpu {
				if container.Name == key {
					containerAvgCpu = RoundUPAndStringify(value, "cpu")
				}
			}
		}

		if resourceValues.AvgMem != nil {
			for key, value := range resourceValues.AvgMem {
				if container.Name == key {
					containerAvgMem = RoundUPAndStringify(value, "mem")
				}
			}
		}

		if resourceValues.PeakCPU != nil {
			for key, value := range resourceValues.PeakCPU {
				if container.Name == key {
					containerPeakCpu = RoundUPAndStringify(value, "cpu")
				}
			}
		}

		if resourceValues.PeakMem != nil {
			for key, value := range resourceValues.PeakMem {
				if container.Name == key {
					containerPeakMem = RoundUPAndStringify(value, "mem")
				}
			}
		}

		slog.WithFields(slog.Fields{"Event": "ContainerAllocationUpdate", "Source": "Webhook"}).Info(fmt.Sprintf("Container %s resources being updated for pod owned by ownerreference in namespace %s. ", controllerName, pod.Namespace))

		container.Resources = core.ResourceRequirements{
			Requests: core.ResourceList{
				"cpu":    resource.MustParse(containerAvgCpu),
				"memory": resource.MustParse(containerAvgMem),
			},
			Limits: core.ResourceList{
				"cpu":    resource.MustParse(containerPeakCpu),
				"memory": resource.MustParse(containerPeakMem),
			},
		}
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (a *podWebhook) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}
