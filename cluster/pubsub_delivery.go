package cluster

import (
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/grpc"
	"github.com/asynkron/protoactor-go/log"
)

var pubsubMemberDeliveryLogThrottle = actor.NewThrottle(10, time.Second, func(i int32) {
	plog.Warn("[PubSubMemberDeliveryActor] Throttled logs", log.Int("count", int(i)))
})

type PubSubMemberDeliveryActor struct {
	subscriberTimeout time.Duration
}

func NewPubSubMemberDeliveryActor(subscriberTimeout time.Duration) *PubSubMemberDeliveryActor {
	return &PubSubMemberDeliveryActor{
		subscriberTimeout: subscriberTimeout,
	}
}

func (p *PubSubMemberDeliveryActor) Receive(c actor.Context) {
	if batch, ok := c.Message().(*DeliverBatchRequest); ok {
		topicBatch := &PubSubAutoRespondBatch{Envelopes: batch.PubSubBatch.Envelopes}
		siList := batch.Subscribers.Subscribers

		invalidDeliveries := make([]*SubscriberDeliveryReport, 0, len(siList))

		type futureWithIdentity struct {
			future   *actor.Future
			identity *SubscriberIdentity
		}
		futureList := make([]futureWithIdentity, 0, len(siList))
		for _, identity := range siList {
			f := p.DeliverBatch(c, topicBatch, identity)
			if f != nil {
				futureList = append(futureList, futureWithIdentity{future: f, identity: identity})
			}
		}

		for _, fWithIdentity := range futureList {
			_, err := fWithIdentity.future.Result()
			identityLog := func(err error) {
				if pubsubMemberDeliveryLogThrottle() == actor.Open {
					if fWithIdentity.identity.GetPid() != nil {
						plog.Info("Pub-sub message delivered to PID", log.String("pid", fWithIdentity.identity.GetPid().String()))
					} else if fWithIdentity.identity.GetClusterIdentity() != nil {
						plog.Info("Pub-sub message delivered to cluster identity", log.String("cluster identity", fWithIdentity.identity.GetClusterIdentity().String()))
					}
				}
			}

			status := DeliveryStatus_Delivered
			if err != nil {
				switch err {
				case actor.ErrTimeout, grpc.ErrTimeout:
					identityLog(err)
					status = DeliveryStatus_Timeout
				case actor.ErrDeadLetter, grpc.ErrDeadLetter:
					identityLog(err)
					status = DeliveryStatus_SubscriberNoLongerReachable
				default:
					identityLog(err)
					status = DeliveryStatus_OtherError
				}
			}
			if status != DeliveryStatus_Delivered {
				invalidDeliveries = append(invalidDeliveries, &SubscriberDeliveryReport{Status: status, Subscriber: fWithIdentity.identity})
			}
		}

		if len(invalidDeliveries) > 0 {
			cluster := GetCluster(c.ActorSystem())
			// we use cluster.Call to locate the topic actor in the cluster
			_, _ = cluster.Call(batch.Topic, TopicActorKind, &NotifyAboutFailingSubscribersRequest{InvalidDeliveries: invalidDeliveries})
		}
	}
}

// DeliverBatch delivers PubSubAutoRespondBatch to SubscriberIdentity.
func (p *PubSubMemberDeliveryActor) DeliverBatch(c actor.Context, batch *PubSubAutoRespondBatch, s *SubscriberIdentity) *actor.Future {
	if pid := s.GetPid(); pid != nil {
		return p.DeliverToPid(c, batch, pid)
	}
	if ci := s.GetClusterIdentity(); ci != nil {
		return p.DeliverToClusterIdentity(c, batch, ci)
	}
	return nil
}

// DeliverToPid delivers PubSubAutoRespondBatch to PID.
func (p *PubSubMemberDeliveryActor) DeliverToPid(c actor.Context, batch *PubSubAutoRespondBatch, pid *actor.PID) *actor.Future {
	return c.RequestFuture(pid, batch, p.subscriberTimeout)
}

// DeliverToClusterIdentity delivers PubSubAutoRespondBatch to ClusterIdentity.
func (p *PubSubMemberDeliveryActor) DeliverToClusterIdentity(c actor.Context, batch *PubSubAutoRespondBatch, ci *ClusterIdentity) *actor.Future {
	cluster := GetCluster(c.ActorSystem())
	// deliver to virtual actor
	// delivery should always be possible, since a virtual actor always exists
	pid := cluster.Get(ci.Identity, ci.Kind)
	return c.RequestFuture(pid, batch, p.subscriberTimeout)
}
