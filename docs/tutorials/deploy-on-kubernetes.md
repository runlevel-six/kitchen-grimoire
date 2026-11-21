# Deploy on Kubernetes behind single sign-on

In this tutorial you'll run Kitchen Grimoire on a Kubernetes cluster at
`https://recipes.example.com`, with people signing in through an SSO
forward-auth proxy. You'll write a small Kustomize overlay on top of the
generic base in `deploy/base`, let your household's groups in, check that
nobody can get around the sign-in, and take a first backup.

The examples use [Authelia](https://www.authelia.com/) as the SSO server
and [ingress-nginx](https://kubernetes.github.io/ingress-nginx/)-style
annotations for forward auth. Any proxy that authenticates each request
and passes the user in headers works the same way; only the annotations
change.

## What you need

- A Kubernetes cluster with a default StorageClass, and `kubectl` with
  Kustomize (built in since kubectl 1.14).
- A CNI that **enforces NetworkPolicy** (Calico, Cilium, and most others;
  plain flannel does not). The sign-in boundary depends on it.
- An ingress controller with forward-auth support, here ingress-nginx in
  the namespace `ingress-nginx`.
- Authelia already working for your other apps at
  `https://auth.example.com`, with your household in two groups: `family`
  and `admins`.
- DNS for `recipes.example.com` pointing at the ingress controller, and a
  TLS certificate (for example from cert-manager) in a Secret named
  `recipes-example-com-tls`.

If you haven't tried Kitchen Grimoire yet, do
[Your first run](first-run.md) first; it's quicker.

## How the pieces fit

```text
phone ──HTTPS──▶ ingress-nginx ──"is this request signed in?"──▶ Authelia
                      │  ◀── 200 + Remote-User, Remote-Name, Remote-Email, Remote-Groups
                      ▼
               grimoire pod :8080   (believes those headers only from the
                                     ingress controller's pod addresses)
               grimoire pod :9090   (metrics and backups; never exposed)
```

Kitchen Grimoire has no login page of its own. It trusts the identity
headers, but **only** when the connection comes from an address in
`GRIMOIRE_TRUSTED_PROXIES`. In a cluster, the proxy's address is a pod IP,
so that setting is the **pod network**, and a NetworkPolicy has to make
sure that the ingress controller is the only pod that can connect.
[Security model](../explanation/security-model.md) explains why both are
needed.

## 1. Find your cluster's addresses

The ingress controller connects to the app from its pod's IP. Find the
range your pods get addresses from:

```sh
kubectl get nodes -o jsonpath='{range .items[*]}{.spec.podCIDR}{"\n"}{end}'
```

Each node prints its slice, such as `10.244.0.0/24`, `10.244.1.0/24`. Use
the range that covers them all, here `10.244.0.0/16`. (Some CNIs don't fill
in `podCIDR`; then check your CNI's configuration, or look at pod IPs with
`kubectl get pods -A -o wide`.)

Then check the ingress controller's namespace and pod labels:

```sh
kubectl get pods -n ingress-nginx --show-labels
```

This tutorial assumes the namespace `ingress-nginx` and the label
`app.kubernetes.io/name=ingress-nginx`.

## 2. Write the overlay

Keep your overlay in your own (private) repository; it holds your real
hostnames. Make a directory `overlays/recipes` with five files.

`kustomization.yaml` pulls the base from GitHub at a pinned release and
pins the image to the same version:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: recipes
resources:
  - namespace.yaml
  - https://github.com/runlevel-six/kitchen-grimoire//deploy/base?ref=v0.4.0
images:
  - name: ghcr.io/runlevel-six/kitchen-grimoire
    newTag: 0.4.0
patches:
  - path: config.yaml
  - path: ingress.yaml
  - path: networkpolicy.yaml
    target:
      kind: NetworkPolicy
      name: grimoire
```

(With a clone of this repository next to it, `../../deploy/base` works as
well.)

`namespace.yaml`:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: recipes
```

`config.yaml` sets the public address, the proxy's range, and the admin
group:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: grimoire-config
data:
  GRIMOIRE_BASE_URL: "https://recipes.example.com"
  # The pod network the ingress controller's pods connect from.
  GRIMOIRE_TRUSTED_PROXIES: "10.244.0.0/16"
  GRIMOIRE_ADMIN_GROUPS: "admins"
```

`ingress.yaml` sets the host and TLS, and asks Authelia about every
request. `auth-response-headers` copies the four identity headers from
Authelia's answer onto the request to the app:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: grimoire
  annotations:
    nginx.ingress.kubernetes.io/auth-method: GET
    nginx.ingress.kubernetes.io/auth-url: "http://authelia.authelia.svc.cluster.local/api/authz/auth-request"
    nginx.ingress.kubernetes.io/auth-signin: "https://auth.example.com?rm=$request_method"
    nginx.ingress.kubernetes.io/auth-response-headers: "Remote-User,Remote-Name,Remote-Email,Remote-Groups"
spec:
  ingressClassName: nginx
  tls:
    - hosts: [recipes.example.com]
      secretName: recipes-example-com-tls
  rules:
    - host: recipes.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: grimoire
                port:
                  name: http
```

Adjust `auth-url` to wherever your Authelia Service lives. The base's
`proxy-body-size: 20m` annotation (room for phone photos) is kept.

`networkpolicy.yaml` replaces the base's placeholder so that only the
ingress controller's pods can reach the app port:

```yaml
# Admit only the ingress controller's pods to the app port.
- op: replace
  path: /spec/ingress/0/from
  value:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: ingress-nginx
      podSelector:
        matchLabels:
          app.kubernetes.io/name: ingress-nginx
```

Check that it all renders:

```sh
kubectl kustomize overlays/recipes | less
```

You should see a Namespace, a ConfigMap, two PVCs (`grimoire-data`,
`grimoire-backup`), two Services (`grimoire`, `grimoire-admin`), a
Deployment, a backup CronJob, two NetworkPolicies, and the Ingress.

The base NetworkPolicy also limits the app's outgoing traffic to DNS in
`kube-system` and to ports 80 and 443 on the internet, excluding the
private ranges (`10/8`, `172.16/12`, `192.168/16`, `100.64/10`, link-local,
loopback). If your DNS runs elsewhere, or your cluster's pod or service
ranges are outside those, patch the egress rules too.

## 3. Let your household in at Authelia

Add rules for the new host to Authelia's `access_control`. Rules are
checked in order and the first match wins, so the narrow bypasses come
first:

```yaml
access_control:
  default_policy: deny
  rules:
    # Health checks for an external uptime monitor. Exact paths only.
    - domain: recipes.example.com
      resources:
        - '^/healthz$'
        - '^/readyz$'
      policy: bypass

    # The PWA manifest and icons (see below).
    - domain: recipes.example.com
      resources:
        - '^/static/manifest\.webmanifest([?].*)?$'
        - '^/static/img/[^/]+$'
      policy: bypass

    # Everyone in family or admins may use the app.
    - domain: recipes.example.com
      subject:
        - 'group:family'
        - 'group:admins'
      policy: one_factor
```

Use `two_factor` instead of `one_factor` if you prefer. People in other
groups are refused by `default_policy: deny`.

**Why the icon and manifest bypass matters.** Browsers fetch a web app's
manifest *without* cookies, and phones often fetch home-screen icons the
same way. Behind forward auth, those requests would get a redirect to the
sign-in page instead of the file, and "Add to Home Screen" would install a
bookmark with a blank icon, or nothing. These files contain no data: the
app serves everything under `/static/` without sign-in anyway.

The Kubernetes liveness and readiness probes don't go through Authelia (the
kubelet calls the pod directly), so the health bypass is only for a monitor
outside the cluster. Keep bypass rules to these exact paths: everything
else in the app needs a signed-in user.

Restart or reload Authelia, and if you can, check the policy with
`authelia access-control check-policy` for a `family` user and for someone
in neither group.

## 4. Deploy

```sh
kubectl apply -k overlays/recipes
kubectl -n recipes rollout status deployment/grimoire
kubectl -n recipes logs deployment/grimoire
```

The log shows `starting`, one `applied migration` line per migration
(the database is created on the new volume), and `listening` on `:8080` and
`:9090`. If the configuration is wrong, the pod exits instead and the log
lists every problem.

## 5. Sign in

Open `https://recipes.example.com` in a browser. Authelia asks you to sign
in, then Kitchen Grimoire shows **No recipes yet**. The footer says
**Signed in as** and your name: the app has created your user from the
headers.

If you're in `admins`, check the admin role: add a recipe, move it to the
trash, and open **Trash**. Admins see **Empty the trash** at the bottom.
See [Make someone an admin](../how-to/make-someone-an-admin.md).

## 6. Prove the boundary holds

Try to impersonate someone by talking to the app directly from another pod,
skipping the proxy:

```sh
kubectl run spoof -n default --rm -it --restart=Never --image=curlimages/curl -- \
  curl -sS -m 5 -o /dev/null -w '%{http_code}\n' \
  -H 'Remote-User: someone-else' -H 'Remote-Groups: admins' \
  http://grimoire.recipes.svc.cluster.local/
```

It must **time out** (the NetworkPolicy drops the connection). If it prints
`200`, your CNI isn't enforcing NetworkPolicy, and any pod in the cluster
could sign in as anyone: fix that before you put recipes in.

Also check that the only Ingress points at `grimoire`, and that
`grimoire-admin` is a ClusterIP Service with no Ingress:

```sh
kubectl -n recipes get ingress,svc
```

## 7. Take the first backup

The base includes a nightly backup CronJob. Run it once now:

```sh
kubectl -n recipes create job --from=cronjob/grimoire-backup grimoire-backup-first
kubectl -n recipes logs -f job/grimoire-backup-first
```

It prints a line like `wrote /backup/grimoire-20261121T031500Z.tar.gz (schema 2, 1 recipes, 0 images, 1 files)`.
Now read [Back up and restore](../how-to/backup-and-restore.md) and set up
an off-cluster copy: the backup volume is on the same storage as the data.

## 8. Put it on your phone

Open `https://recipes.example.com` on your phone, sign in, and add it to the
home screen (Safari: **Share → Add to Home Screen**; Chrome: **⋮ → Add to
Home screen**). Over HTTPS everything works: the app icon, offline reading
of recipes you've opened, and the screen staying on in cook mode.

## What you've built

- One pod running `grimoire serve`, with its SQLite database and photos on
  the `grimoire-data` volume, never more than one replica.
- Sign-in by Authelia, for the `family` and `admins` groups, with
  `admins` also admins in the app.
- A trust boundary of two parts: the app believes identity headers only
  from the pod network, and the NetworkPolicy lets only the ingress
  controller in.
- Nightly backups through the admin port, which nothing outside the
  namespace can reach.

Next: [Operate](../how-to/operate.md) covers logs, metrics, and routine
care; [Upgrade](../how-to/upgrade.md) covers new versions. Not on
Kubernetes? `deploy/compose/compose.yaml` has the same setup for Docker
Compose; put your SSO proxy in front of it and set
`GRIMOIRE_TRUSTED_PROXIES` to the proxy's address.
