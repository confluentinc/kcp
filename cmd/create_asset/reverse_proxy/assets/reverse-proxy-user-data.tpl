#!/bin/bash

sudo apt update -y
sudo apt install -y nginx

load_module /usr/lib/nginx/modules/ngx_stream_module.so;

# Update the NGINX configuration file (/etc/nginx/nginx.conf) to use SNI from incoming TLS sessions for routing traffic to the appropriate servers on ports 443 and 9092.
cat <<EOL | sudo tee /etc/nginx/nginx.conf
load_module /usr/lib/nginx/modules/ngx_stream_module.so;
events {}
stream {
    map \$ssl_preread_server_name \$targetBackend { default \$ssl_preread_server_name; }

    # On lookup failure, reconfigure to use the cloud provider's resolver
    # resolver 169.254.169.253; # for AWS
    # resolver 168.63.129.16;  # for Azure
    # resolver 169.254.169.254;  # for Google

    server {
        listen 9092;

        proxy_connect_timeout 1s;
        proxy_timeout 7200s;

        resolver 169.254.169.253;

        proxy_pass \$targetBackend:9092;
        ssl_preread on;
    }

    server {
        listen 443;

        proxy_connect_timeout 1s;
        proxy_timeout 7200s;

        resolver 169.254.169.253;

        proxy_pass \$targetBackend:443;
        ssl_preread on;
    }

    log_format stream_routing '[\$time_local] remote address \$remote_addr with SNI name "\$ssl_preread_server_name" proxied to "\$upstream_addr" \$protocol \$status \$bytes_sent \$bytes_received \$session_time';
    access_log /var/log/nginx/stream-access.log stream_routing;
}
EOL

# Restart NGINX to apply the changes
sudo systemctl restart nginx
EOF