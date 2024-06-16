package api

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type EventHandler struct {
	Name      string
	Namespace string
	FirstTime bool
	Sig       chan struct{}
}

func (e *EventHandler) OnAdd(obj interface{}, isInInitialList bool) {
	if !e.match(obj) {
		return
	}
	if e.FirstTime {
		e.Sig <- struct{}{}
		e.FirstTime = false
	}
	if !isInInitialList {
		e.Sig <- struct{}{}
	}
}

func (e *EventHandler) OnUpdate(oldObj, newObj interface{}) {
	if !e.match(oldObj) {
		return
	}
	if oldObj.(client.Object).GetResourceVersion() != newObj.(client.Object).GetResourceVersion() {
		e.Sig <- struct{}{}
	}
}

func (e *EventHandler) OnDelete(obj interface{}) {
	if !e.match(obj) {
		return
	}
	e.Sig <- struct{}{}
}

func (e *EventHandler) match(obj interface{}) bool {
	if e.Name != "" && obj.(client.Object).GetName() != e.Name {
		return false
	}
	if e.Namespace != "" && obj.(client.Object).GetNamespace() != e.Namespace {
		return false
	}
	return true
}
