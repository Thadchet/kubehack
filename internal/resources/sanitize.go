package resources

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// sanitize trims objects before they enter the informer cache. It saves memory
// on large clusters, and it keeps Secret values out of the cache and off the
// watch stream. The last-applied annotation has to go too, because on a Secret
// it holds a full copy of the values. Detail views fetch the object on demand.
func sanitize(gvr schema.GroupVersionResource) cache.TransformFunc {
	isSecret := gvr.Group == "" && gvr.Resource == "secrets"
	return func(obj any) (any, error) {
		item, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return obj, nil
		}

		item.SetManagedFields(nil)
		if annotations := item.GetAnnotations(); annotations != nil {
			delete(annotations, lastAppliedAnnotation)
			item.SetAnnotations(annotations)
		}
		if isSecret {
			redactSecret(item)
		}
		return item, nil
	}
}

// redactSecret blanks the values and keeps the key names, which the UI lists.
func redactSecret(item *unstructured.Unstructured) {
	delete(item.Object, "stringData")
	data, ok := item.Object["data"].(map[string]any)
	if !ok {
		return
	}
	for key := range data {
		data[key] = ""
	}
}
