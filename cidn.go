package httpmirror

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"

	"github.com/OpenCIDN/cidn/pkg/apis/task/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

// getBlobName generates a blob name from a URL path using MD5 hash.
func getBlobName(urlPath string) string {
	m := md5.Sum([]byte(urlPath))
	return hex.EncodeToString(m[:])
}

// cacheFileWithCIDN caches a file using CIDN blob management.
// It creates or monitors a CIDN Blob resource to handle the caching operation.
func (m *MirrorHandler) cacheFileWithCIDN(ctx context.Context, sourceFile, cacheFile string) error {
	blobs := m.CIDNClient.TaskV1alpha1().Blobs()
	name := getBlobName(cacheFile)

	blob, err := m.CIDNBlobInformer.Lister().Get(name)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			if m.Logger != nil {
				m.Logger.Println("Error getting blob from informer:", err)
			}
			return err
		}

		blob, err = blobs.Create(ctx, &v1alpha1.Blob{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Annotations: map[string]string{
					v1alpha1.BlobDisplayNameAnnotation: sourceFile,
				},
			},
			Spec: v1alpha1.BlobSpec{
				MaximumRunning:   10,
				MinimumChunkSize: 128 * 1024 * 1024,
				Source: []v1alpha1.BlobSource{
					{
						URL: sourceFile,
					},
				},
				Destination: []v1alpha1.BlobDestination{
					{
						Name:         m.CIDNDestination,
						Path:         cacheFile,
						SkipIfExists: true,
					},
				},
			},
		}, metav1.CreateOptions{})
		if err != nil &&
			!apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	switch blob.Status.Phase {
	case v1alpha1.BlobPhaseSucceeded:
		return nil
	case v1alpha1.BlobPhaseFailed:
		errorMsg := "blob sync failed"
		for _, condition := range blob.Status.Conditions {
			if condition.Message != "" {
				errorMsg = condition.Message
				break
			}
		}
		return fmt.Errorf("failed: %s: %w", errorMsg, ErrNotOK)
	}

	// Create a channel to receive blob status updates
	statusChan := make(chan *v1alpha1.Blob, 1)
	defer close(statusChan)

	// Add event handler to watch for blob status changes
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			blob, ok := obj.(*v1alpha1.Blob)
			if !ok {
				return
			}
			if blob.Name == name {
				statusChan <- blob
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldBlob, ok := oldObj.(*v1alpha1.Blob)
			if !ok {
				return
			}
			newBlob, ok := newObj.(*v1alpha1.Blob)
			if !ok {
				return
			}
			if newBlob.Name == name && oldBlob.Status.Phase != newBlob.Status.Phase {
				statusChan <- newBlob
			}
		},
		DeleteFunc: func(obj interface{}) {
			statusChan <- nil
		},
	}

	rer, err := m.CIDNBlobInformer.Informer().AddEventHandler(handler)
	if err != nil {
		return err
	}

	defer m.CIDNBlobInformer.Informer().RemoveEventHandler(rer)

	for {
		select {
		case updatedBlob, ok := <-statusChan:
			if !ok {
				return fmt.Errorf("blob was canceled before completion")
			}
			if updatedBlob == nil {
				return fmt.Errorf("blob was deleted before completion")
			}
			switch updatedBlob.Status.Phase {
			case v1alpha1.BlobPhaseSucceeded:
				return nil
			case v1alpha1.BlobPhaseFailed:
				errorMsg := "blob sync failed"
				for _, condition := range updatedBlob.Status.Conditions {
					if condition.Message != "" {
						errorMsg = condition.Message
						break
					}
				}
				return fmt.Errorf("failed: %s: %w", errorMsg, ErrNotOK)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
