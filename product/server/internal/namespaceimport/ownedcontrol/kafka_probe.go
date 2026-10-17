package ownedcontrol

import (
	"context"
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// KafkaProbe is migration-only. It uses actual broker metadata/end offsets and
// committed NEXT offsets, not a consumer object's last local message or a SHA
// provided by an operator. Authentication is the dedicated owned fixture/admin
// identity; it is never serialized in observations or diagnostics.
type KafkaProbe struct {
	brokers        []string
	cluster        string
	parts          []namespaceimport.KafkaPartitionIdentity
	user, password string
}
type kafkaSample struct {
	at   time.Time
	rows []kafkaRow
}
type kafkaRow struct {
	Identity      namespaceimport.KafkaPartitionIdentity
	LeaderEpoch   int32
	End           int64
	CommittedNext int64
	GroupState    string
	Members       int
}

func loopbackBroker(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return false
	}
	p, err := strconv.Atoi(port)
	ip := net.ParseIP(host)
	return err == nil && p > 0 && p <= 65535 && ip != nil && ip.IsLoopback()
}
func NewKafkaProbe(brokers []string, cluster string, parts []namespaceimport.KafkaPartitionIdentity, user, password string) (*KafkaProbe, error) {
	if len(brokers) == 0 || cluster == "" || user == "" || password == "" || len(parts) == 0 {
		return nil, ErrOwnedEvidence
	}
	for _, b := range brokers {
		if !loopbackBroker(b) {
			return nil, ErrOwnedEvidence
		}
	}
	seen := map[string]bool{}
	planes := map[string]bool{}
	for _, p := range parts {
		key := p.Topic + "/" + p.Group + "/" + strconv.Itoa(int(p.Partition))
		if p.Topic == "" || p.Group == "" || p.Partition < 0 || seen[key] || p.Plane != "producer-to-allocation" && p.Plane != "allocation-to-persistence" {
			return nil, ErrOwnedEvidence
		}
		seen[key] = true
		planes[p.Plane] = true
	}
	if len(planes) != 2 {
		return nil, ErrOwnedEvidence
	}
	return &KafkaProbe{append([]string(nil), brokers...), cluster, append([]namespaceimport.KafkaPartitionIdentity(nil), parts...), user, password}, nil
}
func (p *KafkaProbe) config() *sarama.Config {
	c := sarama.NewConfig()
	c.Version = sarama.V2_5_0_0
	c.ClientID = "owned-namespace-import-probe-v1"
	c.Net.DialTimeout = 2 * time.Second
	c.Net.ReadTimeout = 2 * time.Second
	c.Net.WriteTimeout = 2 * time.Second
	c.Metadata.Retry.Max = 0
	c.Metadata.RefreshFrequency = 0
	c.Admin.Retry.Max = 0
	c.Net.SASL.Enable = true
	c.Net.SASL.Mechanism = sarama.SASLTypePlaintext
	c.Net.SASL.User = p.user
	c.Net.SASL.Password = p.password
	return c
}
func (p *KafkaProbe) sample(ctx context.Context, client sarama.Client, empty bool) (kafkaSample, error) {
	if err := ctx.Err(); err != nil {
		return kafkaSample{}, err
	}
	controller, err := client.Controller()
	if err != nil {
		return kafkaSample{}, err
	}
	topics := []string{}
	seen := map[string]bool{}
	for _, r := range p.parts {
		if !seen[r.Topic] {
			topics = append(topics, r.Topic)
			seen[r.Topic] = true
		}
	}
	sort.Strings(topics)
	metadata, err := controller.GetMetadata(&sarama.MetadataRequest{Version: 7, Topics: topics, AllowAutoTopicCreation: false})
	if err != nil || metadata.ClusterID == nil || *metadata.ClusterID != p.cluster {
		return kafkaSample{}, ErrOwnedEvidence
	}
	for _, b := range metadata.Brokers {
		if !loopbackBroker(b.Addr()) {
			return kafkaSample{}, ErrOwnedEvidence
		}
	}
	epochs := map[string]map[int32]int32{}
	for _, t := range metadata.Topics {
		if t.Err != sarama.ErrNoError || epochs[t.Name] != nil {
			return kafkaSample{}, ErrOwnedEvidence
		}
		epochs[t.Name] = map[int32]int32{}
		for _, part := range t.Partitions {
			if part.Err != sarama.ErrNoError || part.LeaderEpoch < 0 {
				return kafkaSample{}, ErrOwnedEvidence
			}
			epochs[t.Name][part.ID] = part.LeaderEpoch
		}
	}
	// All current partitions of each declared topic must be represented; an
	// operator cannot omit a partition that still has unpersisted queued records.
	for topic, all := range epochs {
		configured := map[int32]bool{}
		for _, r := range p.parts {
			if r.Topic == topic {
				configured[r.Partition] = true
			}
		}
		if len(configured) != len(all) {
			return kafkaSample{}, ErrOwnedEvidence
		}
		for id := range all {
			if !configured[id] {
				return kafkaSample{}, ErrOwnedEvidence
			}
		}
	}
	result := kafkaSample{}
	for _, r := range p.parts {
		if err := ctx.Err(); err != nil {
			return kafkaSample{}, err
		}
		topic, ok := epochs[r.Topic]
		if !ok {
			return kafkaSample{}, ErrOwnedEvidence
		}
		epoch, ok := topic[r.Partition]
		if !ok {
			return kafkaSample{}, ErrOwnedEvidence
		}
		end, err := client.GetOffset(r.Topic, r.Partition, sarama.OffsetNewest)
		if err != nil || end < 0 {
			return kafkaSample{}, ErrOwnedEvidence
		}
		broker, err := client.Coordinator(r.Group)
		if err != nil {
			return kafkaSample{}, err
		}
		request := &sarama.OffsetFetchRequest{Version: 7, ConsumerGroup: r.Group, RequireStable: true}
		request.AddPartition(r.Topic, r.Partition)
		response, err := broker.FetchOffset(request)
		if err != nil || response.Err != sarama.ErrNoError {
			return kafkaSample{}, ErrOwnedEvidence
		}
		block := response.GetBlock(r.Topic, r.Partition)
		if block == nil || block.Err != sarama.ErrNoError || block.Offset < 0 || block.Offset != end {
			return kafkaSample{}, ErrOwnedEvidence
		}
		groups, err := broker.DescribeGroups(&sarama.DescribeGroupsRequest{Version: 3, Groups: []string{r.Group}})
		if err != nil || len(groups.Groups) != 1 {
			return kafkaSample{}, ErrOwnedEvidence
		}
		g := groups.Groups[0]
		if g.GroupId != r.Group || g.Err != sarama.ErrNoError || empty && (g.State != "Empty" || len(g.Members) != 0) || !empty && g.State != "Stable" && g.State != "Empty" {
			return kafkaSample{}, ErrOwnedEvidence
		}
		result.rows = append(result.rows, kafkaRow{r, epoch, end, block.Offset, g.State, len(g.Members)})
	}
	result.at = time.Now().UTC()
	return result, nil
}

// ObserveDrained is used before consumer Stop (empty=false) and again AFTER
// actual joins/credential exclusion (empty=true). It does not stop a consumer or
// revoke credentials itself; the control agent must enforce that stage order.
func (p *KafkaProbe) ObserveDrained(ctx context.Context, empty bool, minimumInterval time.Duration) (result []namespaceimport.DrainedPartition, resultErr error) {
	if p == nil || ctx == nil || minimumInterval < 200*time.Millisecond || minimumInterval > 5*time.Second {
		return nil, ErrOwnedEvidence
	}
	client, err := sarama.NewClient(p.brokers, p.config())
	if err != nil {
		return nil, err
	}
	defer func() {
		if client.Close() != nil {
			result = nil
			resultErr = ErrOwnedEvidence
		}
	}()
	a, err := p.sample(ctx, client, empty)
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(minimumInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	b, err := p.sample(ctx, client, empty)
	if err != nil || len(a.rows) != len(b.rows) {
		return nil, ErrOwnedEvidence
	}
	for i, row := range a.rows {
		next := b.rows[i]
		if row.Identity != next.Identity || row.LeaderEpoch != next.LeaderEpoch || row.End != next.End || row.CommittedNext != next.CommittedNext || row.GroupState != next.GroupState || row.Members != next.Members {
			return nil, ErrOwnedEvidence
		}
		body, err := json.Marshal(struct {
			Cluster               string
			Previous              kafkaRow
			Current               kafkaRow
			PreviousAt, CurrentAt time.Time
		}{p.cluster, row, next, a.at, b.at})
		if err != nil {
			return nil, err
		}
		result = append(result, namespaceimport.DrainedPartition{Identity: row.Identity, LeaderEpoch: row.LeaderEpoch, PreviousEnd: row.End, CurrentEnd: next.End, CommittedNext: next.CommittedNext, PreviousSampleAt: a.at, CurrentSampleAt: b.at, BrokerObservationRecord: "live-kafka:" + sha(body)})
	}
	return result, nil
}
