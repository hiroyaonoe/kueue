/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package core

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/kueue/pkg/constants"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1alpha1"
	"sigs.k8s.io/kueue/pkg/queue"
)

// QueueReconciler reconciles a Queue object
type QueueReconciler struct {
	client     client.Client
	log        logr.Logger
	queues     *queue.Manager
	wlUpdateCh chan event.GenericEvent
}

func NewQueueReconciler(client client.Client, queues *queue.Manager) *QueueReconciler {
	return &QueueReconciler{
		log:        ctrl.Log.WithName("queue-reconciler"),
		queues:     queues,
		client:     client,
		wlUpdateCh: make(chan event.GenericEvent, wlUpdateChBuffer),
	}
}

func (r *QueueReconciler) NotifyWorkloadUpdate(w *kueue.Workload) {
	r.wlUpdateCh <- event.GenericEvent{Object: w}
}

// kubebuilderのタグを見ると何のリソースを操作するor見るか分かり易い
// queue, eventしか更新しない
//+kubebuilder:rbac:groups="",resources=events,verbs=create;watch;update
//+kubebuilder:rbac:groups=kueue.x-k8s.io,resources=queues,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kueue.x-k8s.io,resources=queues/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kueue.x-k8s.io,resources=queues/finalizers,verbs=update

// Reconcile はqueueの持つworkloadの数を取得し、変更があるならばstatusをupdate
func (r *QueueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var queueObj kueue.Queue
	if err := r.client.Get(ctx, req.NamespacedName, &queueObj); err != nil {
		// we'll ignore not-found errors, since there is nothing to do.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log := ctrl.LoggerFrom(ctx).WithValues("queue", klog.KObj(&queueObj))
	ctx = ctrl.LoggerInto(ctx, log)
	log.V(2).Info("Reconciling Queue")

	// Shallow copy enough for now.
	// 今のstatusの定義ならば、shallow copy = deep copy なのでshallowで十分
	// statusのフィールドが増えた場合はこれが成り立たなくなる場合がある
	oldStatus := queueObj.Status

	// queueの持っている(= pendingしている)workloadの数を取得
	pending, err := r.queues.PendingWorkloads(&queueObj)
	if err != nil {
		r.log.Error(err, "Failed to retrieve queue status")
		return ctrl.Result{}, err
	}

	queueObj.Status.PendingWorkloads = pending
	if !equality.Semantic.DeepEqual(oldStatus, queueObj.Status) {
		err := r.client.Status().Update(ctx, &queueObj)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func (r *QueueReconciler) Create(e event.CreateEvent) bool {
	q, match := e.Object.(*kueue.Queue)
	if !match {
		// No need to interact with the queue manager for other objects.
		return true
	}
	log := r.log.WithValues("queue", klog.KObj(q))
	log.V(2).Info("Queue create event")
	ctx := logr.NewContext(context.Background(), log)
	if err := r.queues.AddQueue(ctx, q); err != nil {
		log.Error(err, "Failed to add queue to system")
	}
	return true
}

func (r *QueueReconciler) Delete(e event.DeleteEvent) bool {
	q, match := e.Object.(*kueue.Queue)
	if !match {
		// No need to interact with the queue manager for other objects.
		return true
	}
	r.log.V(2).Info("Queue delete event", "queue", klog.KObj(q))
	r.queues.DeleteQueue(q)
	return true
}

func (r *QueueReconciler) Update(e event.UpdateEvent) bool {
	q, match := e.ObjectNew.(*kueue.Queue)
	if !match {
		// No need to interact with the queue manager for other objects.
		return true
	}
	log := r.log.WithValues("queue", klog.KObj(q))
	log.V(2).Info("Queue update event")
	if err := r.queues.UpdateQueue(q); err != nil {
		log.Error(err, "Failed to update queue in system")
	}
	return true
}

func (r *QueueReconciler) Generic(e event.GenericEvent) bool {
	r.log.V(3).Info("Got Workload event", "workload", klog.KObj(e.Object))
	return true
}

// qWorkloadHandler signals the controller to reconcile the Queue associated
// to the workload in the event.
// Since the events come from a channel Source, only the Generic handler will
// receive events.
type qWorkloadHandler struct{}

func (h *qWorkloadHandler) Create(event.CreateEvent, workqueue.RateLimitingInterface) {
}

func (h *qWorkloadHandler) Update(event.UpdateEvent, workqueue.RateLimitingInterface) {
}

func (h *qWorkloadHandler) Delete(event.DeleteEvent, workqueue.RateLimitingInterface) {
}

// cq と同様の状況で呼び出される
func (h *qWorkloadHandler) Generic(e event.GenericEvent, q workqueue.RateLimitingInterface) {
	w := e.Object.(*kueue.Workload)
	if w.Name == "" {
		return
	}
	// reconcileのリクエスト
	// queueの名前をworkloadのspecから取得
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      w.Spec.QueueName,
			Namespace: w.Namespace,
		},
	}
	// 1秒後に突っ込む
	// なぜ1秒後なのかはUpdatesBatchPeriodのコメント参照
	q.AddAfter(req, constants.UpdatesBatchPeriod)
}

// SetupWithManager sets up the controller with the Manager.
func (r *QueueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueue.Queue{}).
		Watches(&source.Channel{Source: r.wlUpdateCh}, &qWorkloadHandler{}). //cq reconcileと同様にworkloadのupdate時にqWorkloadHanderを呼び出す
		WithEventFilter(r).
		Complete(r)
}
