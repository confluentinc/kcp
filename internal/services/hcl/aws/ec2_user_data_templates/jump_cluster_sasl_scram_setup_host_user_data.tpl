#!/bin/bash

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

cat << 'EOF' > /home/ec2-user/wait-for-hosts-ready.yml
---
- name: Wait for jump cluster instances to be SSH accessible and ready
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
      retries: 20
      delay: 15
      until: ssh_test.rc == 0
    
    - name: Wait for Python 3 to be available
      raw: python3 --version
      register: python3_check
      failed_when: false
      changed_when: false
      retries: 20
      delay: 15
      until: python3_check.rc == 0
    
    - name: Wait for Python 3.11 to be installed
      raw: python3.11 --version
      register: python311_check
      failed_when: false
      changed_when: false
      retries: 20
      delay: 15
      until: python311_check.rc == 0
    
    - name: Fail if Python 3.11 is not available after retries
      fail:
        msg: "Python 3.11 is NOT installed on {{ inventory_hostname }} after waiting"
      when: python311_check.rc != 0
    
    - name: Wait for packaging module to be installed
      raw: python3.11 -c "import packaging; print('packaging version:', packaging.__version__)"
      register: packaging_check
      failed_when: false
      changed_when: false
      retries: 10
      delay: 15
      until: packaging_check.rc == 0
    
    - name: Fail if packaging module is not installed after retries
      fail:
        msg: "packaging module is NOT installed on {{ inventory_hostname }} after waiting"
      when: packaging_check.rc != 0
    
    - name: Wait for PyYAML module to be installed
      raw: python3.11 -c "import yaml; print('PyYAML version:', yaml.__version__)"
      register: pyyaml_check
      failed_when: false
      changed_when: false
      retries: 10
      delay: 15
      until: pyyaml_check.rc == 0
    
    - name: Fail if PyYAML module is not installed after retries
      fail:
        msg: "PyYAML module is NOT installed on {{ inventory_hostname }} after waiting"
      when: pyyaml_check.rc != 0
    
    - name: Test Ansible ping module with Python 3.11 (verify Python modules work)
      ping:
      register: ansible_ping_test
      failed_when: false
      changed_when: false
      retries: 5
      delay: 5
      until: ansible_ping_test.ping is defined
    
    - name: Fail if Ansible cannot use Python 3.11
      fail:
        msg: "Ansible cannot connect using Python 3.11 on {{ inventory_hostname }}"
      when: ansible_ping_test.ping is not defined
    
    - name: Display readiness status
      debug:
        msg: "{{ inventory_hostname }} is ready with Python {{ python311_check.stdout }}, packaging {{ packaging_check.stdout }}, PyYAML {{ pyyaml_check.stdout }}"
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

ansible-playbook -i hosts.yml wait-for-hosts-ready.yml
ansible -i hosts.yml all -m ping
ansible-playbook -i hosts.yml confluent.platform.validate_hosts
ansible-playbook -i hosts.yml confluent.platform.all --tags=kafka_controller,kafka_broker,schema_registry
ansible-playbook -i hosts.yml cluster-link-setup.yml
