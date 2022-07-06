package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	ranv1alpha1 "github.com/openshift-kni/cluster-group-upgrades-operator/api/v1alpha1"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type TemplateResolver struct {
	//Client          client.Client
	Ctx context.Context
	//Log             logr.Logger
	CguR            *ClusterGroupUpgradeReconciler
	Cgu             *ranv1alpha1.ClusterGroupUpgrade
	LookupNamespace string
}

func (t *TemplateResolver) ResolveTemplate(tmplJSON []byte, tmplContext interface{}) error {
	// Build map of supported template functions
	funcMap := template.FuncMap{
		"fromConfigMap": t.fromConfigMap,
	}

	// Create template processor and initialize function map
	tmpl := template.New("tmpl").Delims("{{hub", "hub}}").Funcs(funcMap)

	// convert the JSON to YAML
	templateYAMLBytes, err := jsonToYAML(tmplJSON)
	t.CguR.Log.Info("ANGIE: objection template yaml string", "content", string(templateYAMLBytes))
	if err != nil {
		return fmt.Errorf("failed to convert the policy template to YAML: %w", err)
	}
	tmpl, err = tmpl.Parse(string(templateYAMLBytes))
	if err != nil {
		return fmt.Errorf("failed to parse the template JSON string %s: %s", string(tmplJSON), err)
	}

	var buf strings.Builder

	err = tmpl.Execute(&buf, tmplContext)
	if err != nil {
		tmplJSONStr := string(tmplJSON)
		return fmt.Errorf("failed to resolve the template %v: %w", tmplJSONStr, err)
	}

	resolvedTemplateStr := buf.String()
	resolvedTemplateBytes, err := yamlToJSON([]byte(resolvedTemplateStr))
	if err != nil {
		t.CguR.Log.Error(err, "err unmarshal")
		return err
	}

	var transformedTemplate interface{}
	if jsonErr := json.Unmarshal(resolvedTemplateBytes, &transformedTemplate); jsonErr != nil {
		t.CguR.Log.Error(jsonErr, "Could not unmarshal data from JSON")
		return err
	}
	//t.CguR.Log.Info("ANGIE: resolvedTemplate", "content", transformedTemplate)
	return nil
}

func (t *TemplateResolver) fromConfigMap(namespace string, cmapname string, key string) (string, error) {
	t.CguR.Log.Info("ANGIE: find hub template resource", "ns", namespace, "name", cmapname, "key", key)

	ns := namespace
	if namespace == "" {
		ns = t.LookupNamespace
	}
	cm := &corev1.ConfigMap{}
	err := t.CguR.Get(t.Ctx, types.NamespacedName{Namespace: ns, Name: cmapname}, cm)
	if err != nil {
		return "", err
	}

	if ns != t.Cgu.GetNamespace() {

		copiedCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cm.GetName(),
				Namespace: t.Cgu.GetNamespace(),
			},
			Data:       cm.Data,
			BinaryData: cm.BinaryData,
		}

		if err := controllerutil.SetControllerReference(t.Cgu, copiedCM, t.CguR.Scheme); err != nil {
			return "", err
		}
		if err := t.CguR.Create(t.Ctx, copiedCM); err != nil {
			if errors.IsAlreadyExists(err) {
				err = t.CguR.Update(t.Ctx, copiedCM)
				if err != nil {
					t.CguR.Log.Error(err, "Fail to update config map", "name", copiedCM.Name, "namespace", copiedCM.Namespace)
					return "", err
				}
			}
			t.CguR.Log.Error(err, "Fail to create config map", "name", copiedCM.Name, "namespace", copiedCM.Namespace)
			return "", err
		}
	}

	/*
		re := regexp.MustCompile(`(.*fromConfigMap)\s"(.*?)"\s"(.+?)"(.*)`)
		for _, tmpl := range t.HubTemplates {

		}*/

	return "", nil
}

func jsonToYAML(j []byte) ([]byte, error) {
	// Convert the JSON to an object
	var jsonObj interface{}

	err := yaml.Unmarshal(j, &jsonObj)
	if err != nil {
		return nil, err // nolint:wrapcheck
	}

	// Marshal this object into YAML
	var b bytes.Buffer
	yamlEncoder := yaml.NewEncoder(&b)
	yamlEncoder.SetIndent(2)

	err = yamlEncoder.Encode(&jsonObj)
	if err != nil {
		return nil, err // nolint:wrapcheck
	}

	return b.Bytes(), nil
}

// yamlToJSON converts YAML to JSON.
func yamlToJSON(y []byte) ([]byte, error) {
	// Convert the YAML to an object.
	var yamlObj interface{}

	err := yaml.Unmarshal(y, &yamlObj)
	if err != nil {
		return nil, err // nolint:wrapcheck
	}

	// Convert this object to JSON
	return json.Marshal(yamlObj) // nolint:wrapcheck
}
