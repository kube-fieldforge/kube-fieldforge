/*
Copyright 2024 Patrick Uiterwijk <patrick@puiterwijk.org>.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"

	configbuilderv1alpha1 "github.com/puiterwijk/kube-configbuilder/api/v1alpha1"
)

// Definitions to manage status conditions
const (
	fieldManagerName = "configbuild-controller"

	// typeGotSourcesConfigBuild represents whether we got all the source data.
	typeGotSourcesConfigBuild = "GotSources"
	// typeUpdatedTargetConfigBuild represents whether the target object was updated.
	typeUpdatedTargetConfigBuild = "UpdatedTarget"

	updateTime = 2 * time.Minute
)

// ConfigBuildReconciler reconciles a ConfigBuild object
type ConfigBuildReconciler struct {
	client.Client
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme
}

//+kubebuilder:rbac:groups=configbuilder.puiterwijk.org,resources=configbuilds,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=configbuilder.puiterwijk.org,resources=configbuilds/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=configbuilder.puiterwijk.org,resources=configbuilds/finalizers,verbs=update

func (r *ConfigBuildReconciler) reportFieldLookupError(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild, description string, fieldName string, err error) error {
	var reason string
	var message string

	if apierrors.IsNotFound(err) {
		reason = "SourceNotFound"
		message = fmt.Sprintf("object referenced in %s %s not found", description, fieldName)
	} else {
		reason = "ErrorRetrivingSource"
		message = fmt.Sprintf("error retriving value referenced in %s %s: %v", description, fieldName, err)
	}
	meta.SetStatusCondition(
		&cbuild.Status.Conditions,
		metav1.Condition{
			Type:    typeGotSourcesConfigBuild,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		},
	)
	if err := r.Status().Update(ctx, cbuild); err != nil {
		l.Error(err, "Failed to update ConfigBuild status")
		return err
	}
	return nil
}

func (r *ConfigBuildReconciler) lookupObjectFieldString(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild, description string, fieldName string, objectType string, objectName string, objectKey string) (string, bool, error) {
	objName := types.NamespacedName{Name: objectName, Namespace: cbuild.Namespace}

	switch objectType {
	case "ConfigMap":
		obj := &corev1.ConfigMap{}
		if err := r.Get(ctx, objName, obj); err != nil {
			return "", false, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, err)
		}
		value, found := obj.Data[objectKey]
		if !found {
			return "", false, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, fmt.Errorf("requested key %s not found", objectKey))
		}
		return value, true, nil
	case "Secret":
		obj := &corev1.Secret{}
		if err := r.Get(ctx, objName, obj); err != nil {
			return "", false, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, err)
		}
		value, found := obj.StringData[objectKey]
		if !found {
			return "", false, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, fmt.Errorf("requested key %s not found", objectKey))
		}
		return value, true, nil
	default:
		// This should be impossible...
		panic(fmt.Sprintf("Reached unknown object field string type... %s", objectType))
	}
}

func (r *ConfigBuildReconciler) retrieveStringData(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild, lookups map[string]configbuilderv1alpha1.ConfigBuildSpecReference, description string) (*map[string]string, error) {
	result := map[string]string{}

	for key, mapping := range lookups {
		var value string
		var found bool
		var err error
		switch mapping.GetType() {
		case "constant":
			value = mapping.Constant.Value
		case "configmap":
			value, found, err = r.lookupObjectFieldString(ctx, l, cbuild, description, key, "ConfigMap", mapping.ConfigMap.Name, mapping.ConfigMap.Key)
			// If it wasn't found, the calling function will update the object
			if !found || err != nil {
				return nil, err
			}
		case "secret":
			value, found, err = r.lookupObjectFieldString(ctx, l, cbuild, description, key, "Secret", mapping.ConfigMap.Name, mapping.ConfigMap.Key)
			// If it wasn't found, the calling function will update the object
			if !found || err != nil {
				return nil, err
			}
		default:
			return nil, r.reportFieldLookupError(ctx, l, cbuild, description, key, fmt.Errorf("invalid type %s", mapping.GetType()))
		}
		result[key] = value
	}

	return &result, nil
}

func (r *ConfigBuildReconciler) retrieveBinaryData(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild, lookups map[string]configbuilderv1alpha1.ConfigBuildSpecReference, description string) (*map[string][]byte, error) {
	return nil, fmt.Errorf("TODO")
}

func (r *ConfigBuildReconciler) buildNewObject(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild) (client.Object, error) {
	annotations, err := r.retrieveStringData(ctx, l, cbuild, cbuild.Spec.Annotations, "annotations")
	if err != nil || annotations == nil {
		return nil, err
	}
	labels, err := r.retrieveStringData(ctx, l, cbuild, cbuild.Spec.Labels, "labels")
	if err != nil || labels == nil {
		return nil, err
	}
	stringData, err := r.retrieveStringData(ctx, l, cbuild, cbuild.Spec.StringData, "stringData")
	if err != nil || stringData == nil {
		return nil, err
	}
	binaryData, err := r.retrieveBinaryData(ctx, l, cbuild, cbuild.Spec.BinaryData, "binaryData")
	if err != nil || binaryData == nil {
		return nil, err
	}

	objMeta := metav1.ObjectMeta{
		Name:      cbuild.Spec.Target.Name,
		Namespace: cbuild.Namespace,

		Annotations: *annotations,
		Labels:      *labels,
	}

	switch strings.ToLower(cbuild.Spec.Target.Kind) {
	case "configmap":
		return &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: "v1",
			},

			ObjectMeta: objMeta,
			Data:       *stringData,
			BinaryData: *binaryData,
		}, nil
	case "secret":
		return &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},

			ObjectMeta: objMeta,
			StringData: *stringData,
			Data:       *binaryData,
		}, nil
	default:
		return nil, reconcile.TerminalError(
			fmt.Errorf("Invalid target kind %s", cbuild.Spec.Target.Kind),
		)
	}
}

func (r *ConfigBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	cbuild := &configbuilderv1alpha1.ConfigBuild{}
	if err := r.Get(ctx, req.NamespacedName, cbuild); err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			l.Info("ConfigBuild resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		l.Error(err, "Failed to get ConfigBuild")
		return ctrl.Result{}, err
	}
	if cbuild.GetDeletionTimestamp() != nil {
		// The object is about to be deleted - we don't really care about this right now
		return ctrl.Result{}, nil
	}

	if cbuild.Status.Conditions == nil || len(cbuild.Status.Conditions) == 0 {
		meta.SetStatusCondition(
			&cbuild.Status.Conditions,
			metav1.Condition{
				Type:    typeGotSourcesConfigBuild,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			},
		)
		meta.SetStatusCondition(
			&cbuild.Status.Conditions,
			metav1.Condition{
				Type:    typeUpdatedTargetConfigBuild,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			},
		)

		if err := r.Status().Update(ctx, cbuild); err != nil {
			l.Error(err, "Failed to update ConfigBuild status")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, cbuild); err != nil {
			l.Error(err, "Failed to re-fetch ConfigBuild")
			return ctrl.Result{}, err
		}
	}

	newObject, err := r.buildNewObject(ctx, l, cbuild)
	if err != nil {
		l.Error(err, "Failed to construct new object")
		meta.SetStatusCondition(
			&cbuild.Status.Conditions,
			metav1.Condition{
				Type:    typeGotSourcesConfigBuild,
				Status:  metav1.ConditionFalse,
				Reason:  "Failed",
				Message: err.Error(),
			},
		)
		if err := r.Status().Update(ctx, cbuild); err != nil {
			l.Error(err, "Failed to update ConfigBuild status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}
	if newObject == nil {
		l.Info("Not all sources existed. Status should be updated by creator. Re-queueing later")
		return ctrl.Result{
			Requeue: true,
		}, nil
	}

	meta.SetStatusCondition(
		&cbuild.Status.Conditions,
		metav1.Condition{
			Type:    typeGotSourcesConfigBuild,
			Status:  metav1.ConditionTrue,
			Reason:  "SourcesRetrieved",
			Message: "Sources retrieved successfully",
		},
	)
	if err := r.Status().Update(ctx, cbuild); err != nil {
		l.Error(err, "Failed to update ConfigBuild status")
		return ctrl.Result{}, err
	}
	if err := r.Get(ctx, req.NamespacedName, cbuild); err != nil {
		l.Error(err, "Failed to re-fetch ConfigBuild")
		return ctrl.Result{}, err
	}

	l.Info(
		"Creating a new object",
		"Object.Kind", newObject.GetObjectKind(),
		"Object.Namespace", newObject.GetNamespace(),
		"Object.Name", newObject.GetName(),
		"Object", newObject,
	)
	force := true
	if err = r.Patch(
		ctx,
		newObject,
		client.Apply,
		&client.PatchOptions{
			Force:        &force,
			FieldManager: fieldManagerName,
		},
	); err != nil {
		l.Error(
			err,
			"Failed to create new Object",
			"Object.Kind", newObject.GetObjectKind(),
			"Object.Namespace", newObject.GetNamespace(),
			"Object.Name", newObject.GetName(),
		)
		meta.SetStatusCondition(
			&cbuild.Status.Conditions,
			metav1.Condition{
				Type:    typeUpdatedTargetConfigBuild,
				Status:  metav1.ConditionFalse,
				Reason:  "Failed",
				Message: err.Error(),
			},
		)
		if err := r.Status().Update(ctx, cbuild); err != nil {
			l.Error(err, "Failed to update ConfigBuild status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	l.Info(
		"Created a new object",
		"Object.Kind", newObject.GetObjectKind(),
		"Object.Namespace", newObject.GetNamespace(),
		"Object.Name", newObject.GetName(),
	)
	meta.SetStatusCondition(
		&cbuild.Status.Conditions,
		metav1.Condition{
			Type:    typeUpdatedTargetConfigBuild,
			Status:  metav1.ConditionTrue,
			Reason:  "TargetUpdated",
			Message: "Target object successfully updated",
		},
	)
	if err := r.Status().Update(ctx, cbuild); err != nil {
		l.Error(err, "Failed to update ConfigBuild status")
		return ctrl.Result{}, err
	}
	if err := r.Get(ctx, req.NamespacedName, cbuild); err != nil {
		l.Error(err, "Failed to re-fetch ConfigBuild")
		return ctrl.Result{}, err
	}
	r.Recorder.Event(cbuild, "Normal", "UpdatedTarget", "Updated target object")

	return ctrl.Result{
		//Requeue:      true,
		//RequeueAfter: updateTime,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configbuilderv1alpha1.ConfigBuild{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
