package main

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/whzghb/kube-apiserver-proxy/pkg/api"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

func main() {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		NewClient: func(config *rest.Config, options client.Options) (client.Client, error) {
			return client.New(config, client.Options{
				Cache: &client.CacheOptions{Unstructured: true, Reader: options.Cache.Reader},
			})
		},
	})
	succeedOrDie(err)

	go func() {
		succeedOrDie(mgr.Start(context.Background()))
	}()

	r := gin.Default()
	a := api.NewApi(mgr)

	apis := r.Group("/apis")
	{
		apis.GET("/:group/:version/:resource", a.GetObjectList)
		apis.GET("/:group/:version/:resource/:name", a.GetObject)
		apis.GET("/:group/:version/namespaces/:namespace/:resource", a.GetObjectList)
		apis.GET("/:group/:version/namespaces/:namespace/:resource/:name", a.GetObject)
		apis.POST("/:group/:version/:resource", a.CreateObject)
		apis.POST("/:group/:version/namespaces/:namespace/:resource", a.CreateObject)
		apis.PUT("/:group/:version/:resource/:name", a.UpdateObject)
		apis.PUT("/:group/:version/namespaces/:namespace/:resource/:name", a.UpdateObject)
		apis.PATCH("/:group/:version/:resource/:name", a.PatchObject)
		apis.PATCH("/:group/:version/namespaces/:namespace/:resource/:name", a.PatchObject)
		apis.DELETE("/:group/:version/:resource/:name", a.DeleteObject)
		apis.DELETE("/:group/:version/namespaces/:namespace/:resource/:name", a.DeleteObject)
	}
	core := r.Group("/api")
	{
		core.GET("/:version/:resource", a.GetObjectList)
		core.GET("/:version/:resource/:name", a.GetObject)
		core.GET("/:version/namespaces/:namespace/:resource", a.GetObjectList)
		core.GET("/:version/namespaces/:namespace/:resource/:name", a.GetObject)
		core.POST("/:version/:resource", a.CreateObject)
		core.POST("/:version/namespaces/:namespace/:resource", a.CreateObject)
		core.PUT("/:version/:resource/:name", a.UpdateObject)
		core.PUT("/:version/namespaces/:namespace/:resource/:name", a.UpdateObject)
		core.PATCH("/:version/:resource/:name", a.PatchObject)
		core.PATCH("/:version/namespaces/:namespace/:resource/:name", a.PatchObject)
		core.DELETE("/:version/:resource/:name", a.DeleteObject)
		core.DELETE("/:version/namespaces/:namespace/:resource/:name", a.DeleteObject)
	}

	r.Run(":8001")
}

func succeedOrDie(err error) {
	if err != nil {
		panic(err)
	}
}
