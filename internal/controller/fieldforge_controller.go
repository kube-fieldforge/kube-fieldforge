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

	fieldforgev1alpha1 "github.com/kube-fieldforge/kube-fieldforge/api/v1alpha1"
)

// Definitions to manage status conditions
const (
	fieldManagerName = "fieldforge-controller"

	// typeGotSourcesFieldForge represents whether we got all the source data.
	typeGotSourcesFieldForge = "GotSources"
	// typeUpdatedTargetFieldForge represents whether the target object was updated.
	typeUpdatedTargetFieldForge = "UpdatedTarget"

	updateTime = 2 * time.Minute
)

// FieldForgeReconciler reconciles a FieldForge object
type FieldForgeReconciler struct {
	client.Client
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme
}

//+kubebuilder:rbac:groups=fieldforge.puiterwijk.org,resources=fieldforges,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fieldforge.puiterwijk.org,resources=fieldforges/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fieldforge.puiterwijk.org,resources=fieldforges/finalizers,verbs=update

type UserError struct {
	Type    string
	Reason  string
	Message string
}

func (u UserError) Error() string {
	return u.Message
}

func (r *FieldForgeReconciler) reportFieldLookupError(ctx context.Context, l logr.Logger, fforge *fieldforgev1alpha1.FieldForge, description string, fieldName string, objName *types.NamespacedName, err error) error {
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
		Type:    typeGotSourcesFieldForge,
		Reason:  reason,
		Message: message,
	}
}

func (r *FieldForgeReconciler) lookupObjectField(ctx context.Context, l logr.Logger, fforge *fieldforgev1alpha1.FieldForge, description string, fieldName string, objectType string, objectName string, objectKey string, isBinary bool) ([]byte, error) {
	objName := types.NamespacedName{Name: objectName, Namespace: fforge.Namespace}

	var value string
	var bValue []byte
	var found bool

	switch objectType {
	case "ConfigMap":
		obj := &corev1.ConfigMap{}
		if err := r.Get(ctx, objName, obj); err != nil {
			return nil, r.reportFieldLookupError(ctx, l, fforge, description, fieldName, &objName, err)
		}
		value, found = obj.Data[objectKey]
		if !found {
			bValue, found = obj.BinaryData[objectKey]
			if !found {
				return nil, r.reportFieldLookupError(ctx, l, fforge, description, fieldName, &objName, fmt.Errorf("requested key %s not found", objectKey))
			}
		}
	case "Secret":
		obj := &corev1.Secret{}
		if err := r.Get(ctx, objName, obj); err != nil {
			return nil, r.reportFieldLookupError(ctx, l, fforge, description, fieldName, &objName, err)
		}
		value, found = obj.StringData[objectKey]
		if !found {
			bValue, found = obj.Data[objectKey]
			if !found {
				return nil, r.reportFieldLookupError(ctx, l, fforge, description, fieldName, &objName, fmt.Errorf("requested key %s not found", objectKey))
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
			return nil, r.reportFieldLookupError(ctx, l, fforge, description, fieldName, &objName, fmt.Errorf("requested key %s not valid utf-8", objectKey))
		}
	}

	return bValue, nil
}

func (r *FieldForgeReconciler) retrieveData(ctx context.Context, l logr.Logger, fforge *fieldforgev1alpha1.FieldForge, lookups map[string]fieldforgev1alpha1.FieldForgeSpecReference, description string, isBinary bool) (*map[string][]byte, error) {
	result := map[string][]byte{}

	for key, mapping := range lookups {
		var value []byte
		var err error
		switch mapping.GetType() {
		case "constant":
			value = []byte(mapping.Constant.Value)
		case "configmap":
			value, err = r.lookupObjectField(ctx, l, fforge, description, key, "ConfigMap", mapping.ConfigMap.Name, mapping.ConfigMap.Key, isBinary)
			// If it wasn't found, the calling function will update the object
			if err != nil {
				return nil, err
			}
		case "secret":
			value, err = r.lookupObjectField(ctx, l, fforge, description, key, "Secret", mapping.Secret.Name, mapping.Secret.Key, isBinary)
			// If it wasn't found, the calling function will update the object
			if err != nil {
				return nil, err
			}
		default:
			return nil, r.reportFieldLookupError(ctx, l, fforge, description, key, nil, fmt.Errorf("invalid type %s", mapping.GetType()))
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

func (r *FieldForgeReconciler) buildNewObject(ctx context.Context, l logr.Logger, fforge *fieldforgev1alpha1.FieldForge) (client.Object, error) {
	annotations, err := r.retrieveData(ctx, l, fforge, fforge.Spec.Annotations, "annotations", false)
	if err != nil || annotations == nil {
		return nil, err
	}
	labels, err := r.retrieveData(ctx, l, fforge, fforge.Spec.Labels, "labels", false)
	if err != nil || labels == nil {
		return nil, err
	}
	stringData, err := r.retrieveData(ctx, l, fforge, fforge.Spec.StringData, "stringData", false)
	if err != nil || stringData == nil {
		return nil, err
	}
	binaryData, err := r.retrieveData(ctx, l, fforge, fforge.Spec.BinaryData, "binaryData", true)
	if err != nil || binaryData == nil {
		return nil, err
	}

	objMeta := metav1.ObjectMeta{
		Name:      fforge.Spec.Target.Name,
		Namespace: fforge.Namespace,

		Annotations: mappedBytesToMappedString(*annotations),
		Labels:      mappedBytesToMappedString(*labels),
	}

	switch strings.ToLower(fforge.Spec.Target.Kind) {
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
			Type:    typeUpdatedTargetFieldForge,
			Reason:  "InvalidType",
			Message: fmt.Sprintf("Requested type %s is not valid", fforge.Spec.Target.Kind),
		}
	}
}

func (r *FieldForgeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	fforge := &fieldforgev1alpha1.FieldForge{}
	if err := r.Get(ctx, req.NamespacedName, fforge); err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			l.Info("FieldForge resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		l.Error(err, "Failed to get FieldForge")
		return ctrl.Result{}, err
	}
	if fforge.GetDeletionTimestamp() != nil {
		// The object is about to be deleted - we don't really care about this right now
		return ctrl.Result{}, nil
	}

	if fforge.Status.Conditions == nil || len(fforge.Status.Conditions) == 0 {
		meta.SetStatusCondition(
			&fforge.Status.Conditions,
			metav1.Condition{
				Type:    typeGotSourcesFieldForge,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			},
		)
		meta.SetStatusCondition(
			&fforge.Status.Conditions,
			metav1.Condition{
				Type:    typeUpdatedTargetFieldForge,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			},
		)

		if err := r.Status().Update(ctx, fforge); err != nil {
			l.Error(err, "Failed to update FieldForge status")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, fforge); err != nil {
			l.Error(err, "Failed to re-fetch FieldForge")
			return ctrl.Result{}, err
		}
	}

	newObject, err := r.buildNewObject(ctx, l, fforge)
	if newObject != nil {
		if err := ctrl.SetControllerReference(fforge, newObject, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err != nil {
		userErr, isUserErr := err.(UserError)
		if isUserErr {
			l.Info("Object wasn't created due to user error", "error", userErr)
			meta.SetStatusCondition(
				&fforge.Status.Conditions,
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
				&fforge.Status.Conditions,
				metav1.Condition{
					Type:    typeGotSourcesFieldForge,
					Status:  metav1.ConditionFalse,
					Reason:  "Failed",
					Message: err.Error(),
				},
			)
		}
		if err := r.Status().Update(ctx, fforge); err != nil {
			l.Error(err, "Failed to update FieldForge status")
			return ctrl.Result{}, err
		}
		if isUserErr {
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, err
		}
	}

	meta.SetStatusCondition(
		&fforge.Status.Conditions,
		metav1.Condition{
			Type:    typeGotSourcesFieldForge,
			Status:  metav1.ConditionTrue,
			Reason:  "SourcesRetrieved",
			Message: "Sources retrieved successfully",
		},
	)
	if err := r.Status().Update(ctx, fforge); err != nil {
		l.Error(err, "Failed to update FieldForge status")
		return ctrl.Result{}, err
	}
	if err := r.Get(ctx, req.NamespacedName, fforge); err != nil {
		l.Error(err, "Failed to re-fetch FieldForge")
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
			&fforge.Status.Conditions,
			metav1.Condition{
				Type:    typeUpdatedTargetFieldForge,
				Status:  metav1.ConditionFalse,
				Reason:  "Failed",
				Message: err.Error(),
			},
		)
		if err := r.Status().Update(ctx, fforge); err != nil {
			l.Error(err, "Failed to update FieldForge status")
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
		&fforge.Status.Conditions,
		metav1.Condition{
			Type:    typeUpdatedTargetFieldForge,
			Status:  metav1.ConditionTrue,
			Reason:  "TargetUpdated",
			Message: "Target object successfully updated",
		},
	)
	if err := r.Status().Update(ctx, fforge); err != nil {
		l.Error(err, "Failed to update FieldForge status")
		return ctrl.Result{}, err
	}
	if err := r.Get(ctx, req.NamespacedName, fforge); err != nil {
		l.Error(err, "Failed to re-fetch FieldForge")
		return ctrl.Result{}, err
	}
	r.Recorder.Event(fforge, "Normal", "UpdatedTarget", "Updated target object")

	return ctrl.Result{
		//Requeue:      true,
		//RequeueAfter: updateTime,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FieldForgeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fieldforgev1alpha1.FieldForge{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
