package postgres_test

import (
	"context"
	"errors"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// A narrow API double keeps tests independent of client-go's broad generated
// fake dependency graph. Unsupported verbs panic through the embedded interface.
type bootstrapTestClient struct {
	dynamic.Interface
	mu       sync.Mutex
	objects  map[string]*unstructured.Unstructured
	actions  []action
	onDelete func(string, metav1.DeleteOptions) error
}

var bootstrapTestSecrets = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

type action string

func (a action) GetVerb() string                 { return string(a) }
func (f *bootstrapTestClient) Actions() []action { return f.actions }
func (f *bootstrapTestClient) ClearActions()     { f.actions = nil }
func (f *bootstrapTestClient) Resource(g schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	if g != bootstrapTestSecrets {
		panic("unexpected resource")
	}
	return &bootstrapTestResource{f: f}
}

type bootstrapTestResource struct {
	dynamic.NamespaceableResourceInterface
	f         *bootstrapTestClient
	namespace string
}

func (r *bootstrapTestResource) Namespace(n string) dynamic.ResourceInterface {
	return &bootstrapTestResource{f: r.f, namespace: n}
}
func (r *bootstrapTestResource) Get(_ context.Context, name string, _ metav1.GetOptions, _ ...string) (*unstructured.Unstructured, error) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	r.f.actions = append(r.f.actions, "get")
	o := r.f.objects[r.namespace+"/"+name]
	if o == nil {
		return nil, apierrors.NewNotFound(bootstrapTestSecrets.GroupResource(), name)
	}
	return o.DeepCopy(), nil
}
func (r *bootstrapTestResource) Create(_ context.Context, o *unstructured.Unstructured, _ metav1.CreateOptions, _ ...string) (*unstructured.Unstructured, error) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	r.f.actions = append(r.f.actions, "create")
	key := r.namespace + "/" + o.GetName()
	if r.f.objects[key] != nil {
		return nil, apierrors.NewAlreadyExists(bootstrapTestSecrets.GroupResource(), o.GetName())
	}
	o = o.DeepCopy()
	o.SetUID("exact-uid")
	o.SetResourceVersion("1")
	r.f.objects[key] = o
	return o.DeepCopy(), nil
}
func (r *bootstrapTestResource) Delete(_ context.Context, name string, opts metav1.DeleteOptions, _ ...string) error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	r.f.actions = append(r.f.actions, "delete")
	if r.f.onDelete != nil {
		if err := r.f.onDelete(name, opts); err != nil {
			return err
		}
	}
	key := r.namespace + "/" + name
	o := r.f.objects[key]
	if o == nil {
		return apierrors.NewNotFound(bootstrapTestSecrets.GroupResource(), name)
	}
	p := opts.Preconditions
	if p == nil || p.UID == nil || *p.UID != o.GetUID() || p.ResourceVersion == nil || *p.ResourceVersion != o.GetResourceVersion() {
		return apierrors.NewConflict(bootstrapTestSecrets.GroupResource(), name, errors.New("bootstrap deletion conflict"))
	}
	delete(r.f.objects, key)
	return nil
}
