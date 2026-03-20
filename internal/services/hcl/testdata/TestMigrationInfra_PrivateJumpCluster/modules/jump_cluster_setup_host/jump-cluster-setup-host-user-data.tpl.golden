#!/bin/bash
set -euo pipefail

# Install Ansible with retries (handles dnf lock contention from cloud-init, dnf-makecache, etc.)
for i in $(seq 1 30); do
  echo "Attempting to install Ansible (attempt $i/30)..."
  if dnf install -y ansible 2>&1; then
    echo "Ansible installed successfully"
    break
  fi
  echo "dnf install failed (likely lock contention), retrying in 10s..."
  sleep 10
done
echo 'export ANSIBLE_COLLECTIONS_PATH=/home/ec2-user' >> /home/ec2-user/.bashrc
export ANSIBLE_COLLECTIONS_PATH=/home/ec2-user
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

cat << 'EOF' > /home/ec2-user/wait-for-hosts-ready.yml
---
- name: Wait for SSH and install Python 3.11 on jump cluster instances
  hosts: all
  gather_facts: no
  vars:
    ansible_python_interpreter: /usr/bin/python3.11

  tasks:
    - name: Wait for SSH connection to be available
      raw: echo "SSH connection test"
      register: ssh_test
      failed_when: false
      changed_when: false
      retries: 30
      delay: 15
      until: ssh_test.rc == 0

    - name: Install Python 3.11 and pip
      raw: |
        for i in $(seq 1 30); do
          echo "Attempt $i/30: installing Python 3.11..."
          if dnf install -y python3.11 python3.11-pip 2>&1; then
            echo "Python 3.11 installed successfully"
            break
          fi
          echo "dnf failed (likely lock contention), retrying in 10s..."
          sleep 10
        done
        python3.11 --version
      register: python_install
      changed_when: true

    - name: Install Python modules (packaging, PyYAML, setuptools)
      raw: python3.11 -m pip install packaging PyYAML setuptools
      register: pip_install
      changed_when: true

    - name: Verify Python 3.11
      raw: python3.11 --version
      register: python311_check
      changed_when: false

    - name: Verify all required modules
      raw: python3.11 -c "import packaging, yaml, pkg_resources; print('All modules OK')"
      register: modules_check
      changed_when: false

    - name: Test Ansible ping with Python 3.11
      ping:
      register: ansible_ping_test

    - name: Display readiness status
      debug:
        msg: "{{ inventory_hostname }} is ready: {{ python311_check.stdout | trim }}, modules: {{ modules_check.stdout | trim }}"
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

cd /home/ec2-user

ansible-playbook -i hosts.yml wait-for-hosts-ready.yml
ansible -i hosts.yml all -m ping
ansible-playbook -i hosts.yml confluent.platform.validate_hosts
ansible-playbook -i hosts.yml jar-deployment.yml
ansible-playbook -i hosts.yml confluent.platform.all --tags=kafka_controller,kafka_broker,schema_registry
ansible-playbook -i hosts.yml cluster-link-setup.yml
