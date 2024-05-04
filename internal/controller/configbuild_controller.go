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
	"unicode/utf8"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

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

type UserError struct {
	Type    string
	Reason  string
	Message string
}

func (u UserError) Error() string {
	return u.Message
}

func (r *ConfigBuildReconciler) reportFieldLookupError(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild, description string, fieldName string, objName *types.NamespacedName, err error) error {
	var reason string
	var message string

	if apierrors.IsNotFound(err) {
		reason = "SourceNotFound"
		message = fmt.Sprintf("object %s referenced in %s %s not found", objName, description, fieldName)
	} else {
		reason = "ErrorRetrivingSource"
		message = fmt.Sprintf("error retriving value referenced in %s %s: %v (object %s)", description, fieldName, err, objName)
	}
	return UserError{
		Type:    typeGotSourcesConfigBuild,
		Reason:  reason,
		Message: message,
	}
}

func (r *ConfigBuildReconciler) lookupObjectField(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild, description string, fieldName string, objectType string, objectName string, objectKey string, isBinary bool) ([]byte, error) {
	objName := types.NamespacedName{Name: objectName, Namespace: cbuild.Namespace}

	var value string
	var bValue []byte
	var found bool

	switch objectType {
	case "ConfigMap":
		obj := &corev1.ConfigMap{}
		if err := r.Get(ctx, objName, obj); err != nil {
			return nil, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, &objName, err)
		}
		value, found = obj.Data[objectKey]
		if !found {
			bValue, found = obj.BinaryData[objectKey]
			if !found {
				return nil, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, &objName, fmt.Errorf("requested key %s not found", objectKey))
			}
		}
	case "Secret":
		obj := &corev1.Secret{}
		if err := r.Get(ctx, objName, obj); err != nil {
			return nil, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, &objName, err)
		}
		value, found = obj.StringData[objectKey]
		if !found {
			bValue, found = obj.Data[objectKey]
			if !found {
				return nil, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, &objName, fmt.Errorf("requested key %s not found", objectKey))
			}
		}
	default:
		// This should be impossible...
		panic(fmt.Sprintf("Reached unknown object field string type... %s", objectType))
	}

	if value != "" {
		bValue = []byte(value)
	}
	if !isBinary {
		// This was set on a binary field of the object
		if !utf8.Valid(bValue) {
			return nil, r.reportFieldLookupError(ctx, l, cbuild, description, fieldName, &objName, fmt.Errorf("requested key %s not valid utf-8", objectKey))
		}
	}

	return bValue, nil
}

func (r *ConfigBuildReconciler) retrieveData(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild, lookups map[string]configbuilderv1alpha1.ConfigBuildSpecReference, description string, isBinary bool) (*map[string][]byte, error) {
	result := map[string][]byte{}

	for key, mapping := range lookups {
		var value []byte
		var err error
		switch mapping.GetType() {
		case "constant":
			value = []byte(mapping.Constant.Value)
		case "configmap":
			value, err = r.lookupObjectField(ctx, l, cbuild, description, key, "ConfigMap", mapping.ConfigMap.Name, mapping.ConfigMap.Key, isBinary)
			// If it wasn't found, the calling function will update the object
			if err != nil {
				return nil, err
			}
		case "secret":
			value, err = r.lookupObjectField(ctx, l, cbuild, description, key, "Secret", mapping.Secret.Name, mapping.Secret.Key, isBinary)
			// If it wasn't found, the calling function will update the object
			if err != nil {
				return nil, err
			}
		default:
			return nil, r.reportFieldLookupError(ctx, l, cbuild, description, key, nil, fmt.Errorf("invalid type %s", mapping.GetType()))
		}
		result[key] = value
	}

	return &result, nil
}

func mappedBytesToMappedString(input map[string][]byte) (out map[string]string) {
	// This function just panics if it's not valid utf8 - this should've been caught by the lookup method because !isBinary
	out = make(map[string]string)
	for key, value := range input {
		out[key] = string(value)
	}
	return
}

func (r *ConfigBuildReconciler) buildNewObject(ctx context.Context, l logr.Logger, cbuild *configbuilderv1alpha1.ConfigBuild) (client.Object, error) {
	annotations, err := r.retrieveData(ctx, l, cbuild, cbuild.Spec.Annotations, "annotations", false)
	if err != nil || annotations == nil {
		return nil, err
	}
	labels, err := r.retrieveData(ctx, l, cbuild, cbuild.Spec.Labels, "labels", false)
	if err != nil || labels == nil {
		return nil, err
	}
	stringData, err := r.retrieveData(ctx, l, cbuild, cbuild.Spec.StringData, "stringData", false)
	if err != nil || stringData == nil {
		return nil, err
	}
	binaryData, err := r.retrieveData(ctx, l, cbuild, cbuild.Spec.BinaryData, "binaryData", true)
	if err != nil || binaryData == nil {
		return nil, err
	}

	objMeta := metav1.ObjectMeta{
		Name:      cbuild.Spec.Target.Name,
		Namespace: cbuild.Namespace,

		Annotations: mappedBytesToMappedString(*annotations),
		Labels:      mappedBytesToMappedString(*labels),
	}

	switch strings.ToLower(cbuild.Spec.Target.Kind) {
	case "configmap":
		return &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: "v1",
			},

			ObjectMeta: objMeta,
			Data:       mappedBytesToMappedString(*stringData),
			BinaryData: *binaryData,
		}, nil
	case "secret":
		return &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},

			ObjectMeta: objMeta,
			StringData: mappedBytesToMappedString(*stringData),
			Data:       *binaryData,
		}, nil
	default:
		return nil, UserError{
			Type:    typeUpdatedTargetConfigBuild,
			Reason:  "InvalidType",
			Message: fmt.Sprintf("Requested type %s is not valid", cbuild.Spec.Target.Kind),
		}
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
	if err := ctrl.SetControllerReference(cbuild, newObject, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err != nil {
		userErr, isUserErr := err.(UserError)
		if isUserErr {
			l.Info("Object wasn't created due to user error", "error", userErr)
			meta.SetStatusCondition(
				&cbuild.Status.Conditions,
				metav1.Condition{
					Type:    userErr.Type,
					Status:  metav1.ConditionFalse,
					Reason:  userErr.Reason,
					Message: userErr.Message,
				},
			)
		} else {
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
		}
		if err := r.Status().Update(ctx, cbuild); err != nil {
			l.Error(err, "Failed to update ConfigBuild status")
			return ctrl.Result{}, err
		}
		if isUserErr {
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, err
		}
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
