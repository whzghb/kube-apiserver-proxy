package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/whzghb/kube-apiserver-proxy/pkg/auth"
	http_common "github.com/whzghb/kube-apiserver-proxy/pkg/http-common"
	"io"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"strings"
	"time"
)

// TODO database
const (
	UserName         = "admin"
	Password         = "password"
	DefaultNamespace = "default"
)

type Api struct {
	mgr ctrl.Manager
}

func NewApi(mgr ctrl.Manager) *Api {
	return &Api{mgr: mgr}
}

func (a *Api) Login(c *gin.Context) {
	user := &http_common.UserLoginRequest{}
	err := c.ShouldBind(user)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "bad request", "detail": err.Error()})
		return
	}
	if user.Name != UserName || user.Password != Password {
		c.JSON(http.StatusForbidden, gin.H{"msg": "invalid username or password"})
		return
	}

	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: DefaultNamespace, Name: user.Name}}
	err = a.mgr.GetClient().Get(c.Request.Context(), types.NamespacedName{Name: user.Name, Namespace: DefaultNamespace}, sa)
	if err == nil {
		err = a.mgr.GetClient().Delete(c.Request.Context(), sa)
		if err != nil {
			fmt.Printf("delete serviceaccount error: %s\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
		}
		sa.ResourceVersion = ""
	}
	err = a.mgr.GetClient().Create(c.Request.Context(), sa)
	if err != nil {
		//TODO log
		fmt.Printf("create serviceaccount error: %s\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
		return
	}

	expireTime := int64(3600)
	token := &authv1.TokenRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: user.Name,
		},
		Spec: authv1.TokenRequestSpec{
			ExpirationSeconds: &expireTime,
		},
	}
	err = a.mgr.GetClient().SubResource("token").Create(c.Request.Context(), sa, token)
	if err != nil {
		fmt.Printf("create token error: %s\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
		return
	}
	resp := &http_common.UserLoginResponse{Token: token.Status.Token}
	auth.Cache.Store(token.Status.Token[:32], &auth.UserInfo{Name: user.Name, Namespace: DefaultNamespace, RenewTime: time.Now()})

	c.JSON(http.StatusOK, resp)
}

func (a *Api) Logout(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "bad request", "detail": "name can be set"})
		return
	}
	sa := &corev1.ServiceAccount{}
	err := a.mgr.GetClient().Get(c.Request.Context(), types.NamespacedName{Namespace: DefaultNamespace, Name: name}, sa)
	if err != nil {
		fmt.Printf("get serviceaccount error: %s\n", err)
		c.JSON(http.StatusNotFound, gin.H{"msg": "not found"})
		return
	}
	err = a.mgr.GetClient().Delete(c.Request.Context(), sa)
	if err != nil {
		fmt.Printf("delete serviceaccount error: %s\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
		return
	}

	auth.Cache.Delete(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")[:32])

	c.JSON(http.StatusOK, gin.H{"msg": "success"})
}

func (a *Api) GetObjectList(c *gin.Context) {
	objList, err := a.getUnstructuredObjList(c)
	if err != nil {
		a.errorParseHandler(c, err)
		return
	}
	watch := c.Query("watch")
	if watch == "true" {
		a.WatchList(c)
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
	watch := c.Query("watch")
	if watch == "true" {
		a.WatchGet(c)
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

func (a *Api) WatchGet(c *gin.Context) {
	obj, _ := a.getUnstructuredObj(c)
	informer, err := a.mgr.GetCache().GetInformerForKind(c.Request.Context(), obj.GetObjectKind().GroupVersionKind())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
		return
	}

	e := &EventHandler{FirstTime: true, Sig: make(chan struct{})}
	registration, err := informer.AddEventHandler(e)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
		return
	}
	defer func() {
		a.removeEventHandler(informer, registration)
	}()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-e.Sig:
			err = a.mgr.GetClient().Get(context.Background(), a.getNamespacedName(c), obj)
			if err != nil {
				if apierrors.IsNotFound(err) {
					c.SSEvent("message", nil)
					return true
				}
				return false
			}
			e.Namespace = obj.GetNamespace()
			e.Name = obj.GetName()
			c.SSEvent("message", obj)
		default:
			time.Sleep(1 * time.Second)
		}
		return true
	})
}

func (a *Api) WatchList(c *gin.Context) {
	objList, _ := a.getUnstructuredObjList(c)
	namespace := a.parseNamespace(c)
	listOptions, err := a.parseListOptions(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
		return
	}

	informer, err := a.mgr.GetCache().GetInformerForKind(c.Request.Context(), objList.GetObjectKind().GroupVersionKind())
	e := &EventHandler{FirstTime: true, Sig: make(chan struct{}), Namespace: namespace}
	registration, err := informer.AddEventHandler(e)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
		return
	}
	defer func() {
		a.removeEventHandler(informer, registration)
	}()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-e.Sig:
			if namespace != "" {
				err = a.mgr.GetClient().List(context.Background(), objList, client.InNamespace(namespace), listOptions)
			} else {
				err = a.mgr.GetClient().List(context.Background(), objList, listOptions)
			}
			if err != nil {
				return false
			}
			c.SSEvent("message", objList)
		default:
			time.Sleep(1 * time.Second)
		}
		return true
	})
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

func (a *Api) removeEventHandler(informer cache.Informer, handler toolscache.ResourceEventHandlerRegistration) {
	for {
		err := retry.OnError(retry.DefaultRetry, func(err error) bool {
			return err != nil
		}, func() error {
			return informer.RemoveEventHandler(handler)
		})
		if err == nil {
			fmt.Println("eventHandler removed")
			return
		}
		fmt.Printf("remove eventHandler error: %s\n", err)
	}
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
