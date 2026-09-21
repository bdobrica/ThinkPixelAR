package bootstrap

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// A narrow API double keeps tests independent of client-go's broad generated
// fake dependency graph. Unsupported verbs panic through the embedded interface.
type fakeClient struct {
	dynamic.Interface
	objects  map[string]*unstructured.Unstructured
	actions  []action
	onDelete func(string, metav1.DeleteOptions) error
}
type action string

func (a action) GetVerb() string        { return string(a) }
func (f *fakeClient) Actions() []action { return f.actions }
func (f *fakeClient) ClearActions()     { f.actions = nil }
func (f *fakeClient) Resource(g schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	if g != secrets {
		panic("unexpected resource")
	}
	return &fakeResource{f: f}
}

type fakeResource struct {
	dynamic.NamespaceableResourceInterface
	f         *fakeClient
	namespace string
}

func (r *fakeResource) Namespace(n string) dynamic.ResourceInterface {
	return &fakeResource{f: r.f, namespace: n}
}
func (r *fakeResource) Get(_ context.Context, name string, _ metav1.GetOptions, _ ...string) (*unstructured.Unstructured, error) {
	r.f.actions = append(r.f.actions, "get")
	o := r.f.objects[r.namespace+"/"+name]
	if o == nil {
		return nil, apierrors.NewNotFound(secrets.GroupResource(), name)
	}
	return o.DeepCopy(), nil
}
func (r *fakeResource) Create(_ context.Context, o *unstructured.Unstructured, _ metav1.CreateOptions, _ ...string) (*unstructured.Unstructured, error) {
	r.f.actions = append(r.f.actions, "create")
	key := r.namespace + "/" + o.GetName()
	if r.f.objects[key] != nil {
		return nil, apierrors.NewAlreadyExists(secrets.GroupResource(), o.GetName())
	}
	o = o.DeepCopy()
	o.SetUID("test-uid")
	o.SetResourceVersion("1")
	r.f.objects[key] = o
	return o.DeepCopy(), nil
}
func (r *fakeResource) Delete(_ context.Context, name string, opts metav1.DeleteOptions, _ ...string) error {
	r.f.actions = append(r.f.actions, "delete")
	if r.f.onDelete != nil {
		if err := r.f.onDelete(name, opts); err != nil {
			return err
		}
	}
	key := r.namespace + "/" + name
	o := r.f.objects[key]
	if o == nil {
		return apierrors.NewNotFound(secrets.GroupResource(), name)
	}
	p := opts.Preconditions
	if p == nil || p.UID == nil || *p.UID != o.GetUID() || p.ResourceVersion == nil || *p.ResourceVersion != o.GetResourceVersion() {
		return apierrors.NewConflict(secrets.GroupResource(), name, ErrBootstrap)
	}
	delete(r.f.objects, key)
	return nil
}
