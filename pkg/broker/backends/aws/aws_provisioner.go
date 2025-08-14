//go:build aws

// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	configpb "velda.io/velda/pkg/proto/config"
)

type AwsPoolProvisioner struct {
	schedulerSet    *broker.SchedulerSet
	ssmClient       *ssm.Client
	lastSeenVersion map[string]time.Time
	lastSeenTime    map[string]time.Time
	cfg             *configpb.AWSProvisioner
}

func (p *AwsPoolProvisioner) Run(ctx context.Context) {
	p.lastSeenVersion = make(map[string]time.Time)
	p.lastSeenTime = make(map[string]time.Time)
	go p.run(ctx)
}

func (p *AwsPoolProvisioner) run(ctx context.Context) {
	interval := p.cfg.UpdateInterval.AsDuration()
	if interval == 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	updateLoop := func(t time.Time) {
		var nextToken *string

		for {
			output, err := p.ssmClient.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
				Path:      aws.String(p.cfg.ConfigPrefix + "/"),
				NextToken: nextToken,
			})
			if err != nil {
				log.Printf("Failed to query SSM parameters: %v", err)
				break
			}

			for _, param := range output.Parameters {
				lastVersion, ok := p.lastSeenVersion[*param.Name]
				if !ok || lastVersion.Before(*param.LastModifiedDate) {
					err := p.update(ctx, &param, !ok)
					if err != nil {
						log.Printf("Failed to update pool %s: %v", *param.Name, err)
					}
					p.lastSeenVersion[*param.Name] = *param.LastModifiedDate
				}
				p.lastSeenTime[*param.Name] = t
			}
			nextToken = output.NextToken

			if nextToken == nil {
				break
			}
		}
		for k, v := range p.lastSeenTime {
			if v != t {
				// TODO: Remove pool k
				log.Printf("Pool %s not seen in last update", k)
				delete(p.lastSeenVersion, k)
				delete(p.lastSeenTime, k)
			}
		}
	}
	updateLoop(time.Now())

	for {
		select {
		case t := <-ticker.C:
			updateLoop(t)
		case <-ctx.Done():
			return
		}
	}
}

func (p *AwsPoolProvisioner) update(ctx context.Context, param *ssmtypes.Parameter, new bool) error {
	cfg := param.Value
	obj := &configpb.AgentPool{}
	var yamlObj map[string]interface{}
	if err := yaml.Unmarshal([]byte(*cfg), &yamlObj); err != nil {
		return fmt.Errorf("failed to unmarshal yaml: %w", err)
	}
	jsonData, err := json.Marshal(yamlObj)
	if err != nil {
		return fmt.Errorf("failed to marshal json: %w", err)
	}
	if err := protojson.Unmarshal(jsonData, obj); err != nil {
		return fmt.Errorf("failed to unmarshal protojson: %w", err)
	}
	if p.cfg.ConfigPrefix+"/"+obj.Name != *param.Name {
		return fmt.Errorf("Name mismatch: %s != %s", p.cfg.ConfigPrefix+"/"+obj.Name, *param.Name)
	}
	pool, err := p.schedulerSet.GetOrCreatePool(obj.Name)
	if err != nil {
		return err
	}
	autoscalerConfig, err := backends.AutoScaledConfigFromConfig(ctx, obj)
	if err != nil {
		return err
	}
	if new && obj.AutoScaler.GetInitialDelay() != nil {
		time.AfterFunc(obj.AutoScaler.GetInitialDelay().AsDuration(), pool.PoolManager.ReadyForIdleMaintenance)
	}
	log.Printf("Updating pool from AWS SSM parameter store: %v", obj.Name)
	pool.PoolManager.UpdateConfig(autoscalerConfig)
	return nil
}

type AwsPoolProvisionerFactory struct{}

func (*AwsPoolProvisionerFactory) NewProvisioner(cfg *configpb.Provisioner, schedulers *broker.SchedulerSet) (backends.Provisioner, error) {
	cfgAws := cfg.GetAws()
	awsCfg, err := config.LoadDefaultConfig(context.TODO())

	if err != nil {
		return nil, err
	}
	ssmClient := ssm.NewFromConfig(awsCfg)
	return &AwsPoolProvisioner{
		schedulerSet: schedulers,
		ssmClient:    ssmClient,
		cfg:          cfgAws,
	}, nil
}

func (*AwsPoolProvisionerFactory) CanHandle(cfg *configpb.Provisioner) bool {
	return cfg.GetAws() != nil
}

func init() {
	backends.RegisterProvisioner(&AwsPoolProvisionerFactory{})
}
