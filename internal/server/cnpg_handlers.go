package server

import (
	"errors"
	"log"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/skyhook-io/radar/internal/k8s"
)

const cnpgGroup = "postgresql.cnpg.io"

// CNPGCatalogUser is one Cluster pinned to an image catalog.
type CNPGCatalogUser struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Major is the PostgreSQL major the Cluster asks the catalog for. A
	// reference without one lands here as 0, which the screen must not describe
	// as "asks for PostgreSQL 0".
	Major int `json:"major,omitempty"`
	// Image the Cluster resolved and is running now. A cluster pinned to a
	// catalog carries no spec.imageName, so this is the only place the running
	// image appears.
	Image string `json:"image,omitempty"`
}

// CNPGCatalogUsersResponse lists the Clusters referencing one image catalog.
type CNPGCatalogUsersResponse struct {
	Clusters []CNPGCatalogUser `json:"clusters"`
}

// catalogRefMatches reports whether a Cluster's imageCatalogRef names this
// catalog.
//
// CloudNativePG defaults an omitted `kind` to the namespaced ImageCatalog, so a
// reference without one must not be counted against a ClusterImageCatalog of the
// same name — the two are different objects and may both exist.
func catalogRefMatches(ref map[string]interface{}, name, wantKind string) bool {
	if refName, _ := ref["name"].(string); refName != name {
		return false
	}
	refKind, _ := ref["kind"].(string)
	if refKind == "" {
		refKind = "ImageCatalog"
	}
	return refKind == wantKind
}

// catalogRefMajor reads the PostgreSQL major from a catalog reference.
//
// Read tolerantly: the same field arrives as int64 or float64 depending on how
// the object entered the dynamic cache, and NestedInt64 alone misses the float64
// shape — which would drop a real major to zero, a state the screen treats as
// "the reference carries no major at all".
func catalogRefMajor(ref map[string]interface{}) int {
	switch v := ref["major"].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// handleCNPGCatalogUsers returns the Clusters pinned to an image catalog.
//
//	GET /api/cnpg/imagecatalogs/{namespace}/{name}/clusters
//	GET /api/cnpg/clusterimagecatalogs/{name}/clusters
//
// A ClusterImageCatalog is cluster-scoped and may be referenced from any
// namespace, so the answer's scope is not the subject's. Asking the generic
// resource list without a namespace would inherit the caller's namespace view
// filter — a browsing preference — and report "nothing uses this" on the
// strength of whichever namespaces they happen to be looking at. Scope follows
// permission here, as it does for the RBAC reverse lookups.
func (s *Server) handleCNPGCatalogUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "catalog name is required")
		return
	}
	// Empty for the cluster-scoped route, which is what tells the two apart:
	// a Cluster's imageCatalogRef.kind must match the catalog it names.
	namespace := chi.URLParam(r, "namespace")
	wantKind := "ClusterImageCatalog"
	if namespace != "" {
		wantKind = "ImageCatalog"
	}

	if !s.canRead(r, cnpgGroup, "clusters", namespace, "list") {
		s.writeError(w, http.StatusForbidden, "no access to CloudNativePG clusters")
		return
	}
	cache := k8s.GetResourceCache()
	if cache == nil {
		s.writeError(w, http.StatusServiceUnavailable, "Resource cache not available")
		return
	}
	// A namespaced catalog can only be referenced from its own namespace; a
	// cluster-scoped one from anywhere.
	items, err := listDynamicSynced(r.Context(), cache, "Cluster", cnpgGroup, namespace)
	switch {
	case err == nil:
	case errors.Is(err, k8s.ErrUnknownDynamicKind):
		// No CloudNativePG on this cluster, so nothing can be pinned to a catalog.
		s.writeJSON(w, CNPGCatalogUsersResponse{Clusters: []CNPGCatalogUser{}})
		return
	case errors.Is(err, errDynamicNotSynced):
		s.writeError(w, http.StatusServiceUnavailable, "clusters are still loading")
		return
	default:
		// "No cluster uses this catalog" is the sentence someone reads before
		// editing it. Never say it because the lookup failed.
		log.Printf("[cnpg] Failed to list Clusters for catalog %s/%s: %v", sanitizeForLog(namespace), sanitizeForLog(name), err)
		s.writeError(w, http.StatusServiceUnavailable, "could not read CloudNativePG clusters")
		return
	}

	resp := CNPGCatalogUsersResponse{Clusters: []CNPGCatalogUser{}}
	for _, u := range items {
		if u == nil {
			continue
		}
		ref, found, _ := unstructured.NestedMap(u.Object, "spec", "imageCatalogRef")
		if !found {
			continue
		}
		if !catalogRefMatches(ref, name, wantKind) {
			continue
		}
		user := CNPGCatalogUser{Namespace: u.GetNamespace(), Name: u.GetName()}
		user.Major = catalogRefMajor(ref)
		if img, _, _ := unstructured.NestedString(u.Object, "status", "image"); img != "" {
			user.Image = img
		}
		resp.Clusters = append(resp.Clusters, user)
	}
	sort.Slice(resp.Clusters, func(i, j int) bool {
		if resp.Clusters[i].Namespace != resp.Clusters[j].Namespace {
			return resp.Clusters[i].Namespace < resp.Clusters[j].Namespace
		}
		return resp.Clusters[i].Name < resp.Clusters[j].Name
	})
	s.writeJSON(w, resp)
}
