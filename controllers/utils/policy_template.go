package utils

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type TemplateResolver struct {
	client.Client
	Ctx             context.Context
	Log             logr.Logger
	TargetNamespace string
	LookupNamespace string
}

func (r *TemplateResolver) ResolveHubTemplate(data interface{}) (interface{}, error) {
	var err error

	if dataMap, isMap := data.(map[string]interface{}); isMap {
		for key, value := range dataMap {
			//r.Log.Info("ANGIE: data key is", "key", key)
			//r.Log.Info("ANGIE: data value is", "value", value)
			if dataMap[key], err = r.ResolveHubTemplate(value); err != nil {
				return data, err
			}
		}
	} else if dataSlice, isSlice := data.([]interface{}); isSlice {
		for key, value := range dataSlice {
			if dataSlice[key], err = r.ResolveHubTemplate(value); err != nil {
				return data, err
			}
		}
	} else if dataString, isString := data.(string); isString {
		if strings.Contains(dataString, "{{hub") {
			r.Log.Info("Found hub template in policy", "template", dataString)
			data, err = r.copyHubTemplateResource(dataString)
			if err != nil {
				return data, fmt.Errorf("Failed to resolve hub template: %s, %s", dataString, err)
			}
		}
	}

	return data, nil
}

func (r *TemplateResolver) copyHubTemplateResource(templates string) (string, error) {
	// Regular expression to find out all hub templates in a string
	re1 := regexp.MustCompile(`{{hub\s+.*?\s+hub}}`)
	// Regular expression to get hub template function name, resource name and namespace referenced in the function
	re2 := regexp.MustCompile(`{{hub.*)(fromConfigMap|fromSecret)\s+(\(\s*printf\s(.+?)\s*\)|"(.*?)")\s+(\(\s*printf\s(.+?)\s*\)|"(.*?)")(.*hub}})`)

	var resolvedTemplates = templates
	discoveredTemplates := re1.FindAllString(templates, -1)
	for _, template := range discoveredTemplates {
		matches := re2.FindAllStringSubmatch(template, -1)

		if len(matches) == 0 {
			if strings.Contains(template, "lookup") {
				return "", fmt.Errorf("Template function lookup is not supported in TALM")
			} else if strings.Contains(template, "fromClusterClaim") {
				return "", fmt.Errorf("Template function fromClusterClaim is not supported in TALM")
			}
			return "", fmt.Errorf("Template format is not supported in TALM")
		}

		for _, match := range matches {
			function := match[2]

			if match[4] != "" {
				return "", fmt.Errorf("Printf is not supported in Template function Namespace field")
			}
			namespace := match[5]

			if match[7] != "" {
				return "", fmt.Errorf("Printf is not supported in Template function Name field")
			}
			name := match[8]

			fromNamespace := namespace
			if namespace == "" {
				// namespace is empty
				fromNamespace = r.LookupNamespace
			}

			fromResource := types.NamespacedName{
				Name:      name,
				Namespace: fromNamespace,
			}

			toResource := types.NamespacedName{
				Name:      r.LookupNamespace + "." + name,
				Namespace: r.TargetNamespace,
			}

			if function == "fromConfigMap" {
				if err := r.copyConfigmap(r.Ctx, fromResource, toResource); err != nil {
					return "", err
				}
			} else if function == "fromSecret" {
				if err := r.copySecret(r.Ctx, fromResource, toResource); err != nil {
					return "", err
				}
			}

			// Update the hub templating with the replicated configmap name and namespace
			updatedTemplate := re2.ReplaceAllString(template, `$1$2`+` "`+toResource.Namespace+`"`+` "`+toResource.Name+`"`+`$9`)
			resolvedTemplates = strings.Replace(resolvedTemplates, template, updatedTemplate, -1)
		}
	}

	return resolvedTemplates, nil
}

func (r *TemplateResolver) copyConfigmap(ctx context.Context, fromResource types.NamespacedName, toResource types.NamespacedName) error {
	// Get the original configmap referenced in the inform policy
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, fromResource, cm)
	if err != nil {
		return err
	}

	copiedCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        toResource.Name,
			Namespace:   toResource.Namespace,
			Annotations: cm.GetAnnotations(),
		},
		Data:       cm.Data,
		BinaryData: cm.BinaryData,
		Immutable:  cm.Immutable,
	}
	labels := cm.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["openshift-cluster-group-upgrades/fromCmName"] = cm.GetName()
	labels["openshift-cluster-group-upgrades/fromCmNamespace"] = cm.GetNamespace()
	copiedCM.SetLabels(labels)

	existingCM := &corev1.ConfigMap{}
	if err = r.Get(ctx, toResource, existingCM); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		if err := r.Create(ctx, copiedCM); err != nil {
			r.Log.Error(err, "Fail to create config map", "name", copiedCM.Name, "namespace", copiedCM.Namespace)
			return err
		}
	} else {
		err = r.Update(ctx, copiedCM)
		if err != nil {
			r.Log.Error(err, "Fail to update config map", "name", copiedCM.Name, "namespace", copiedCM.Namespace)
			return err
		}
	}
	return nil
}

func (r *TemplateResolver) copySecret(ctx context.Context, fromResource types.NamespacedName, toResource types.NamespacedName) error {
	// Get the original secret referenced in the inform policy
	secret := &corev1.Secret{}
	err := r.Get(ctx, fromResource, secret)
	if err != nil {
		return err
	}

	copiedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        toResource.Name,
			Namespace:   toResource.Namespace,
			Annotations: secret.GetAnnotations(),
		},
		Data:       secret.Data,
		StringData: secret.StringData,
		Immutable:  secret.Immutable,
		Type:       secret.Type,
	}
	labels := secret.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["openshift-cluster-group-upgrades/fromSecretName"] = secret.GetName()
	labels["openshift-cluster-group-upgrades/fromSecretNamespace"] = secret.GetNamespace()
	copiedSecret.SetLabels(labels)

	existingCM := &corev1.Secret{}
	if err = r.Get(ctx, toResource, existingCM); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		if err := r.Create(ctx, copiedSecret); err != nil {
			r.Log.Error(err, "Fail to create secret", "name", copiedSecret.Name, "namespace", copiedSecret.Namespace)
			return err
		}
	} else {
		err = r.Update(ctx, copiedSecret)
		if err != nil {
			r.Log.Error(err, "Fail to update secret", "name", copiedSecret.Name, "namespace", copiedSecret.Namespace)
			return err
		}
	}
	return nil
}
