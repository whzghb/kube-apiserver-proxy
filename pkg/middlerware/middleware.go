package middlerware

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/whzghb/kube-apiserver-proxy/pkg/auth"
	authv1 "k8s.io/api/authentication/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

var methodVerbMap = map[string]string{
	"GET":    "get",
	"LIST":   "list",
	"POST":   "create",
	"PUT":    "update",
	"PATCH":  "patch",
	"DELETE": "delete",
	"Watch":  "watch",
}

func Auth(mgr ctrl.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.String() == "/user/login" {
			c.Next()
			return
		}

		// 认证
		token := c.GetHeader("Authorization")
		if token == "" {
			c.JSON(http.StatusForbidden, gin.H{"msg": "403 forbidden"})
			c.Abort()
			return
		}

		var name, namespace string
		var valid bool
		authToken := strings.TrimPrefix(token, "Bearer ")

		if cache, ok := auth.Cache.Load(authToken[:32]); ok {
			user := cache.(*auth.UserInfo)
			name, namespace = user.Name, user.Namespace
			if user.RenewTime.Add(5 * time.Second).After(time.Now()) {
				valid = true
			}
		}

		if !valid {
			userName, code, err := authenticate(mgr, authToken)
			if err != nil {
				c.JSON(code, gin.H{"msg": err.Error()})
				c.Abort()
				return
			}
			user := strings.Split(userName, ":")
			name, namespace = user[3], user[2]
			auth.Cache.Store(authToken[:32], &auth.UserInfo{Namespace: namespace, Name: name, RenewTime: time.Now()})
		}

		if strings.HasPrefix(c.Request.URL.String(), "/user/logout") {
			c.Next()
			return
		}

		// 鉴权
		roleBindingList := &rbacv1.RoleBindingList{}
		clusterRoleBindingList := &rbacv1.ClusterRoleBindingList{}

		fieldSelector := fields.OneTermEqualSelector(".subjects[*].name", name)
		err := mgr.GetClient().List(c.Request.Context(), roleBindingList, &client.ListOptions{
			FieldSelector: fieldSelector,
			Namespace:     namespace,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
			c.Abort()
			return
		}

		err = mgr.GetClient().List(c.Request.Context(), clusterRoleBindingList, &client.ListOptions{
			FieldSelector: fieldSelector,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
			c.Abort()
			return
		}

		// system:serviceaccount:default:admin
		rules := make([]rbacv1.PolicyRule, 0, 10)
		for _, roleBinding := range roleBindingList.Items {
			role := &rbacv1.Role{}
			clusterRole := &rbacv1.ClusterRole{}
			if roleBinding.Kind == "ClusterRole" {
				err = mgr.GetClient().Get(c.Request.Context(), types.NamespacedName{Namespace: roleBinding.Namespace, Name: roleBinding.RoleRef.Name}, clusterRole)
			} else {
				err = mgr.GetClient().Get(c.Request.Context(), types.NamespacedName{Namespace: roleBinding.Namespace, Name: roleBinding.RoleRef.Name}, role)
			}
			if err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
				c.Abort()
				return
			}
			if len(role.Rules) != 0 {
				rules = append(rules, role.Rules...)
				continue
			}
			rules = append(rules, clusterRole.Rules...)
		}

		for _, clusterRoleBinding := range clusterRoleBindingList.Items {
			clusterRole := &rbacv1.ClusterRole{}
			err = mgr.GetClient().Get(c.Request.Context(), types.NamespacedName{Name: clusterRoleBinding.RoleRef.Name}, clusterRole)
			if err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				c.JSON(http.StatusInternalServerError, gin.H{"msg": "server error"})
				c.Abort()
				return
			}
			rules = append(rules, clusterRole.Rules...)
		}

		gvr := parseGVR(c)
		resourceName := parseName(c)
		method := c.Request.Method
		for _, rule := range rules {
			groupMatch, verbMatch := false, false
			for _, group := range rule.APIGroups {
				if group != gvr.Group && group != "*" {
					continue
				}
				groupMatch = true
				break
			}
			if !groupMatch {
				continue
			}

			for _, verb := range rule.Verbs {
				if resourceName == "" && c.Request.Method == "GET" {
					method = "LIST"
				}
				if watch := c.Query("watch"); watch == "true" {
					method = "Watch"
				}
				if verb != methodVerbMap[method] && verb != "*" {
					continue
				}
				verbMatch = true
				break
			}
			if !verbMatch {
				continue
			}

			for _, resource := range rule.Resources {
				if resource != gvr.Resource && resource+"s" != gvr.Resource && resource != gvr.Resource+"s" && resource != "*" {
					continue
				}
				c.Next()
				return
			}
		}
		c.JSON(http.StatusForbidden, gin.H{"msg": "403 forbidden"})
		c.Abort()
	}
}

func HeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		watch := c.Query("watch")
		if watch == "" {
			c.Next()
			return
		}
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Next()
	}
}

type GVR struct {
	Group    string
	Version  string
	Resource string
}

func parseGVR(c *gin.Context) GVR {
	return GVR{
		Group:    c.Param("group"),
		Version:  c.Param("version"),
		Resource: c.Param("resource"),
	}
}

func parseName(c *gin.Context) string {
	return c.Param("name")
}

func authenticate(mgr ctrl.Manager, authToken string) (string, int, error) {
	tr := &authv1.TokenReview{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tokenreview",
		},
		Spec: authv1.TokenReviewSpec{
			Token: authToken,
		},
	}

	err := mgr.GetClient().Create(context.Background(), tr)
	if err != nil {
		return "", http.StatusInternalServerError, errors.New("server error")
	}

	if !tr.Status.Authenticated {
		return "", http.StatusForbidden, errors.New("403 forbidden")
	}

	return tr.Status.User.Username, 0, nil
}
