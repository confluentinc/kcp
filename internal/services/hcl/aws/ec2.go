package aws

import (
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

const (
	VarAwsPublicSubnetID                          = "aws_public_subnet_id"
	VarSecurityGroupIDs                           = "security_group_ids"
	VarAwsKeyPairName                             = "aws_key_pair_name"
	VarConfluentPlatformBrokerInstancesPrivateDNS = "confluent_platform_broker_instances_private_dns"
	VarPrivateKey                                 = "private_key"
)

var JumpClusterSetupHostVariables = []types.TerraformVariable{
	{Name: VarAwsPublicSubnetID, Description: "ID of the public subnet for the Ansible control node instance", Sensitive: false, Type: "string"},
	{Name: VarSecurityGroupIDs, Description: "IDs of the security groups for the Ansible control node instance", Sensitive: false, Type: "list(string)"},
	{Name: VarAwsKeyPairName, Description: "Name of the AWS key pair for SSH access to the Ansible control node instance", Sensitive: false, Type: "string"},
	{Name: VarConfluentPlatformBrokerInstancesPrivateDNS, Description: "Private DNS names of the Confluent Platform broker instances", Sensitive: false, Type: "list(string)"},
	{Name: VarPrivateKey, Description: "Private SSH key for accessing the Confluent Platform broker instances", Sensitive: true, Type: "string"},
}

func GenerateAmazonLinuxAMI() *hclwrite.Block {
	dataBlock := hclwrite.NewBlock("data", []string{"aws_ami", "amzn_linux_ami"})
	body := dataBlock.Body()

	body.SetAttributeValue("most_recent", cty.BoolVal(true))
	body.SetAttributeValue("owners", cty.ListVal([]cty.Value{cty.StringVal("137112412989")}))
	body.AppendNewline()

	// Filter for name
	nameFilterBlock := body.AppendNewBlock("filter", nil)
	nameFilterBody := nameFilterBlock.Body()
	nameFilterBody.SetAttributeValue("name", cty.StringVal("name"))
	nameFilterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("al2023-ami-2023.*-kernel-6.1-x86_64")}))
	body.AppendNewline()

	// Filter for state
	stateFilterBlock := body.AppendNewBlock("filter", nil)
	stateFilterBody := stateFilterBlock.Body()
	stateFilterBody.SetAttributeValue("name", cty.StringVal("state"))
	stateFilterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("available")}))
	body.AppendNewline()

	// Filter for architecture
	archFilterBlock := body.AppendNewBlock("filter", nil)
	archFilterBody := archFilterBlock.Body()
	archFilterBody.SetAttributeValue("name", cty.StringVal("architecture"))
	archFilterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("x86_64")}))
	body.AppendNewline()

	// Filter for virtualization-type
	virtFilterBlock := body.AppendNewBlock("filter", nil)
	virtFilterBody := virtFilterBlock.Body()
	virtFilterBody.SetAttributeValue("name", cty.StringVal("virtualization-type"))
	virtFilterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("hvm")}))

	return dataBlock
}

func GenerateJumpClusterSetupHost() *hclwrite.Block {
	resourceBlock := hclwrite.NewBlock("resource", []string{"aws_instance", "jump_cluster_setup_host"})
	body := resourceBlock.Body()

	body.SetAttributeRaw("ami", utils.TokensForResourceReference("data.aws_ami.amzn_linux_ami.id"))
	body.SetAttributeValue("instance_type", cty.StringVal("t2.medium"))
	body.SetAttributeRaw("subnet_id", utils.TokensForVarReference(VarAwsPublicSubnetID))
	body.SetAttributeRaw("vpc_security_group_ids", utils.TokensForVarReference(VarSecurityGroupIDs))
	body.SetAttributeRaw("key_name", utils.TokensForVarReference(VarAwsKeyPairName))
	body.SetAttributeValue("associate_public_ip_address", cty.BoolVal(true))
	body.AppendNewline()

	// Create templatefile function call for user_data
	templatefileMap := map[string]hclwrite.Tokens{
		"broker_ips":  utils.TokensForVarReference(VarConfluentPlatformBrokerInstancesPrivateDNS),
		"private_key": utils.TokensForVarReference(VarPrivateKey),
	}

	// Build templatefile function call: templatefile("${path.module}/ansible-control-node-user-data.tpl", {...})
	templatefileTokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("templatefile")},
		&hclwrite.Token{Type: hclsyntax.TokenOParen, Bytes: []byte("(")},
		&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("${path.module}/ansible-control-node-user-data.tpl")},
		&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
		&hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(", ")},
	}
	templatefileTokens = append(templatefileTokens, utils.TokensForMap(templatefileMap)...)
	templatefileTokens = append(templatefileTokens, &hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")})

	body.SetAttributeRaw("user_data", templatefileTokens)
	body.AppendNewline()

	// Tags as attribute
	tagsMap := map[string]cty.Value{
		"Name": cty.StringVal("jump_cluster_setup_host"),
	}
	body.SetAttributeValue("tags", cty.MapVal(tagsMap))

	return resourceBlock
}

func GenerateJumpClusterSetupHostUserDataTpl() string {
	return `#!/bin/bash

sudo su - ec2-user
cd /home/ec2-user
sudo dnf install -y ansible
echo 'export ANSIBLE_COLLECTIONS_PATH=/home/ec2-user' >> /home/ec2-user/.bashrc
source /home/ec2-user/.bashrc
ansible-galaxy collection install confluent.platform:7.9.1 -p /home/ec2-user
echo "${private_key}" > /home/ec2-user/broker_key_rsa
chown ec2-user:ec2-user /home/ec2-user/broker_key_rsa
chmod 400 /home/ec2-user/broker_key_rsa
echo "[defaults]" > /home/ec2-user/ansible.cfg
echo "hash_behaviour=merge" >> /home/ec2-user/ansible.cfg
echo "host_key_checking=False" >> /home/ec2-user/ansible.cfg

cat << 'EOF' > /home/ec2-user/hosts.yml
kafka_controller:
  hosts:
    ${broker_ips[0]}:

kafka_broker:
  hosts:
%{ for ip in broker_ips ~}
    ${ip}:
%{ endfor ~}

schema_registry:
  hosts:
    ${broker_ips[0]}:

all:
  vars:
    ansible_connection: ssh
    ansible_user: ec2-user
    ansible_become: true
    ansible_ssh_private_key_file: /home/ec2-user/broker_key_rsa
    ansible_python_interpreter: /usr/bin/python3.11
EOF

cat << 'EOF' > /home/ec2-user/verify-dependencies-installed.yml
---
- name: Check Python 3.11 and dependencies
  hosts: all
  become: yes
  vars:
    target_user: ec2-user
    target_home: /home/ec2-user
    ansible_python_interpreter: /usr/bin/python3
    
  tasks:
    - name: Check if Python 3.11 is installed
      command: python3.11 --version
      register: python311_check
      failed_when: false
      changed_when: false
      retries: 5
      delay: 3
      until: python311_check.rc == 0
      
    - name: Fail if Python 3.11 is not installed after retries
      fail:
        msg: "Python 3.11 is NOT installed on {{ inventory_hostname }} after {{ ansible_loop.revs }} attempts"
      when: python311_check.rc != 0
      
    - name: Display Python 3.11 status
      debug:
        msg: "Python 3.11 is installed: {{ python311_check.stdout }}"
        
    - name: Check if packaging module is installed
      command: python3.11 -c "import packaging; print('packaging version:', packaging.__version__)"
      register: packaging_check
      failed_when: false
      changed_when: false
      retries: 3
      delay: 2
      until: packaging_check.rc == 0
      
    - name: Fail if packaging module is not installed after retries
      fail:
        msg: "packaging module is NOT installed on {{ inventory_hostname }} after retries"
      when: packaging_check.rc != 0
      
    - name: Display packaging module status
      debug:
        msg: "packaging module is installed: {{ packaging_check.stdout }}"
      
    - name: Check if PyYAML module is installed
      command: python3.11 -c "import yaml; print('PyYAML version:', yaml.__version__)"
      register: pyyaml_check
      failed_when: false
      changed_when: false
      retries: 3
      delay: 2
      until: pyyaml_check.rc == 0
      
    - name: Fail if PyYAML module is not installed after retries
      fail:
        msg: "PyYAML module is NOT installed on {{ inventory_hostname }} after retries"
      when: pyyaml_check.rc != 0
      
    - name: Display PyYAML module status
      debug:
        msg: "PyYAML module is installed: {{ pyyaml_check.stdout }}"
      
    - name: All dependencies verified
      debug:
        msg: "All dependencies are installed on {{ inventory_hostname }}"

EOF

wget https://github.com/aws/aws-msk-iam-auth/releases/download/v2.3.2/aws-msk-iam-auth-2.3.2-all.jar -P /home/ec2-user/jars

cat << 'EOF' > /home/ec2-user/jar-deployment.yml
---
- name: Distribute custom JARs to Kafka Brokers
  hosts: kafka_broker
  become: true

  tasks:
    - name: Create the directory for the custom JARs
      ansible.builtin.file:
        path: /usr/share/java/kafka
        state: directory
        owner: root
        group: root
        mode: '0755'

    - name: Copy AWS MSK IAM Auth JAR
      ansible.builtin.copy:
        src: /home/ec2-user/jars/aws-msk-iam-auth-2.3.2-all.jar
        dest: /usr/share/java/kafka/aws-msk-iam-auth-2.3.2-all.jar
        owner: root
        group: root
        mode: '0644'

EOF

cat << 'EOF' > /home/ec2-user/cluster-link-setup.yml
---
- name: Post-installation cluster link setup for Confluent Platform
  hosts: kafka_broker
  gather_facts: yes
  
  tasks:
    - name: Wait for Confluent Platform services to be ready
      wait_for:
        port: "{{ item }}"
        host: "{{ inventory_hostname }}"
        timeout: 300
        sleep: 2
      loop:
        - 9092
      tags:
        - health_check

    - name: Wait for Schema Registry (on schema registry hosts)
      wait_for:
        port: 8081
        host: "{{ inventory_hostname }}"
        timeout: 600
      when: inventory_hostname in groups['schema_registry']
      tags:
        - health_check

    - name: Test Kafka broker API connectivity
      shell: |
        timeout 30 kafka-broker-api-versions --bootstrap-server ${broker_ips[0]}:9092 2>/dev/null
      register: kafka_api_test
      retries: 30
      until: kafka_api_test.rc == 0
      tags:
        - health_check

    - name: Verify cluster link script exists
      stat:
        path: /home/ec2-user/create-cluster-links.sh
      register: cluster_link_script
      when: inventory_hostname == groups['kafka_broker'][0]
      
    - name: Execute cluster link creation script
      shell: /home/ec2-user/create-cluster-links.sh 2>/dev/null
      become: yes
      become_user: ec2-user
      when: 
        - inventory_hostname == groups['kafka_broker'][0]
        - cluster_link_script.stat.exists
      register: cluster_link_result
      tags:
        - cluster_link

    - name: Display cluster link creation result
      debug:
        msg: "{{ cluster_link_result.stdout_lines }}"
      when: 
        - inventory_hostname == groups['kafka_broker'][0]
        - cluster_link_result.stdout_lines is defined
      tags:
        - cluster_link

    - name: Verify cluster link was created successfully
      shell: |
        kafka-cluster-links --bootstrap-server ${broker_ips[0]}:9092 --list 2>/dev/null
      become: yes
      become_user: ec2-user
      register: cluster_links_list
      when: inventory_hostname == groups['kafka_broker'][0]
      tags:
        - verification

    - name: Display existing cluster links
      debug:
        var: cluster_links_list.stdout_lines
      when: inventory_hostname == groups['kafka_broker'][0]
      tags:
        - verification

EOF

ansible -i hosts.yml all -m ping
ansible-playbook -i hosts.yml confluent.platform.validate_hosts
ansible-playbook -i hosts.yml verify-dependencies-installed.yml
ansible-playbook -i hosts.yml jar-deployment.yml
ansible-playbook -i hosts.yml confluent.platform.all --tags=kafka_controller,kafka_broker,schema_registry
ansible-playbook -i hosts.yml cluster-link-setup.yml
`
}
