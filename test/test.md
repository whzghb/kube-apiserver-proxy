patch
```bash
curl -v -k -XPATCH  -H "Accept: application/json, */*" -H "Content-Type: application/json" -d @patch.json http://127.0.0.1:8001/apis/apps/v1/namespaces/default/deployments/patch-demo
```