# MSK to Confluent Platform to Confluent Cloud Migration Infrastructure (Public)

## Deployment Steps

Follow these steps in order to deploy the infrastructure:


### 1. Providing required secrets

You have two options when providing secrets to terraform

#### 1a. Set environment variables 

Set the following environment variables for Confluent Cloud authentication:

```bash
export TF_VAR_confluent_cloud_api_key="<YOUR_CC_API_KEY>"
export TF_VAR_confluent_cloud_api_secret="<YOUR_CC_API_SECRET>"
export TF_VAR_msk_sasl_scram_username="<YOUR_MSK_SASL_SCRAM_USERNAME>"
export TF_VAR_msk_sasl_scram_password="<YOUR_MSK_SASL_SCRAM_PASSWORD>"
```

#### 1b. Enter secrets manually

Enter each secret individually when prompted after running `terraform plan | apply`


### 2. Initialize Terraform

```bash
terraform init
```

This downloads the required providers and initializes the working directory.


### 3. Plan the Deployment

```bash
terraform plan
```

Review the planned changes to ensure everything looks correct before applying.


### 4. Apply the Configuration

```bash
terraform apply
```

Type `yes` when prompted to confirm the deployment.


## Cleanup

To destroy the infrastructure:

```bash
terraform destroy
```

**Warning:** This will permanently delete all created resources. Ensure you have backed up any important data before destroying.
