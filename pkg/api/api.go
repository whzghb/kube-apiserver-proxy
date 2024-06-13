package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"strings"
)

type Api struct {
	mgr ctrl.Manager
}

func NewApi(mgr ctrl.Manager) *Api {
	return &Api{mgr: mgr}
}

func (a *Api) GetObjectList(c *gin.Context) {
	objList, err := a.getUnstructuredObjList(c)
	if err != nil {
		a.errorParseHandler(c, err)
		return
	}
	namespace := a.parseNamespace(c)
	listOptions, err := a.parseListOptions(c)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	if namespace != "" {
		err = a.mgr.GetClient().List(context.Background(), objList, client.InNamespace(namespace), listOptions)
	} else {
		err = a.mgr.GetClient().List(context.Background(), objList, listOptions)
	}
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}
	c.JSON(http.StatusOK, objList)
}

func (a *Api) GetObject(c *gin.Context) {
	obj, err := a.getUnstructuredObj(c)
	if err != nil {
		a.errorParseHandler(c, err)
		return
	}

	err = a.mgr.GetClient().Get(context.Background(), a.getNamespacedName(c), obj)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	c.JSON(http.StatusOK, obj)
}

func (a *Api) CreateObject(c *gin.Context) {
	obj := &unstructured.Unstructured{}
	err := c.Bind(obj)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	err = a.mgr.GetClient().Create(context.Background(), obj)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"msg": fmt.Sprintf("%s/%s created", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName())})
}

func (a *Api) DeleteObject(c *gin.Context) {
	obj, err := a.getUnstructuredObj(c)
	if err != nil {
		a.errorParseHandler(c, err)
		return
	}

	err = a.mgr.GetClient().Get(context.Background(), a.getNamespacedName(c), obj)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	err = a.mgr.GetClient().Delete(context.Background(), obj)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"msg": fmt.Sprintf("%s/%s deleted", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName())})
}

func (a *Api) UpdateObject(c *gin.Context) {
	obj := &unstructured.Unstructured{}
	err := c.Bind(obj)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	err = a.mgr.GetClient().Update(context.Background(), obj)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"msg": fmt.Sprintf("%s/%s updated", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName())})
}

func (a *Api) PatchObject(c *gin.Context) {
	obj, err := a.getUnstructuredObj(c)
	if err != nil {
		a.errorParseHandler(c, err)
		return
	}

	err = a.mgr.GetClient().Get(context.Background(), a.getNamespacedName(c), obj)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	err = a.mgr.GetClient().Patch(context.Background(), obj, client.RawPatch(types.StrategicMergePatchType, bodyBytes))
	if err != nil {
		a.errorResponseHandler(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"msg": fmt.Sprintf("%s/%s patched", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName())})
}

func (a *Api) parseGVR(c *gin.Context) (runtimeschema.GroupVersionKind, error) {
	gvk, err := a.mgr.GetRESTMapper().KindFor(runtimeschema.GroupVersionResource{
		Group:    c.Param("group"),
		Version:  c.Param("version"),
		Resource: c.Param("resource"),
	})
	if err != nil {
		return runtimeschema.GroupVersionKind{}, err
	}
	return gvk, nil
}

func (a *Api) parseNamespace(c *gin.Context) string {
	return c.Param("namespace")
}

func (a *Api) parseName(c *gin.Context) string {
	return c.Param("name")
}

func (a *Api) getNamespacedName(c *gin.Context) types.NamespacedName {
	namespacedName := types.NamespacedName{}
	name := a.parseName(c)
	namespace := a.parseNamespace(c)
	if name != "" {
		namespacedName.Name = name
	}
	if namespace != "" {
		namespacedName.Namespace = namespace
	}
	return namespacedName
}

func (a *Api) getUnstructuredObj(c *gin.Context) (*unstructured.Unstructured, error) {
	gvk, err := a.parseGVR(c)
	if err != nil {
		return nil, err
	}

	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(gvk.GroupVersion().String())
	obj.SetKind(gvk.Kind)

	return obj, nil
}

func (a *Api) getUnstructuredObjList(c *gin.Context) (*unstructured.UnstructuredList, error) {
	gvk, err := a.parseGVR(c)
	if err != nil {
		return nil, err
	}

	objList := &unstructured.UnstructuredList{}
	objList.SetAPIVersion(gvk.GroupVersion().String())
	objList.SetKind(gvk.Kind)
	return objList, nil
}

func (a *Api) parseListOptions(c *gin.Context) (*client.ListOptions, error) {
	var err error
	limitNum := 500

	limit := c.Query("limit")
	if limit != "" {
		limitNum, err = strconv.Atoi(limit)
		if err != nil {
			return nil, err
		}
	}

	labelSelector := c.Query("labelSelector")
	if labelSelector == "" {
		return &client.ListOptions{Limit: int64(limitNum), LabelSelector: labels.Everything()}, nil
	}

	selectorList := []string{labelSelector}
	if strings.Contains(labelSelector, ",") {
		selectorList = strings.Split(labelSelector, ",")
	}
	selectors := make(map[string]string, len(selectorList))
	for _, selector := range selectorList {
		keyValues := strings.Split(selector, "=")
		if len(keyValues) != 2 {
			return nil, errors.New("bad request")
		}
		selectors[keyValues[0]] = keyValues[1]
	}
	// TODO filedSelector

	return &client.ListOptions{Limit: int64(limitNum), LabelSelector: labels.SelectorFromValidatedSet(selectors)}, nil
}

func (a *Api) errorResponseHandler(c *gin.Context, err error) {
	if apierrors.IsNotFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"msg": err.Error()})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"msg": err.Error()})
}

func (a *Api) errorParseHandler(c *gin.Context, err error) {
	c.JSON(http.StatusNotFound, gin.H{"msg": err.Error()})
}
