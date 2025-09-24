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
ansible-playbook -i hosts.yml confluent.platform.all --tags=kafka_controller,kafka_broker,schema_registry
ansible-playbook -i hosts.yml cluster-link-setup.yml
