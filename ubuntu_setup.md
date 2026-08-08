ssh -i ~/.ssh/id_rsa root@209.38.210.163  
# Connect to your Ubuntu server via SSH as root.

passwd  
# Change the root user's password by setting a new password.

sudo apt-get update -y && sudo apt-get dist-upgrade -y  
# Update the package list and upgrade all system packages to their latest versions.

curl -fsSL https://get.docker.com -o get-docker.sh && sudo sh get-docker.sh
# Run Docker's official installation script

sudo add-apt-repository ppa:deadsnakes/ppa -y  
# Add the deadsnakes PPA to allow installation of newer Python versions.

sudo apt-get install python3-pip python3-dev libpq-dev postgresql postgresql-contrib nginx curl redis-server supervisor -y  
# Install Python package manager (pip), Python development headers (for compiling packages), PostgreSQL database, Nginx web server, Redis cache, and Supervisor process manager.

apt-get install python3.12 python3.12-venv -y  
# Install Python 3.11 and its virtual environment package support.

sudo apt-get install net-tools  
# Install networking tool suite (contains netstat, ifconfig, etc).


hostnamectl set-hostname fignode  # Requires reboot  
# Set the hostname to "fignode".

hostnamectl status  
# Display current hostname settings.

adduser voxprofit  
# Create a new user named "voxprofit".

sudo usermod -aG sudo voxprofit  
# Add "voxprofit" user to the sudo/admin group.

mkdir -p /home/voxprofit/.ssh  
# Create SSH configuration directory for user "voxprofit".

sudo cp /root/.ssh/authorized_keys /home/voxprofit/.ssh/  
# Copy authorized SSH keys from root user to "voxprofit" user.

sudo chmod -R 700 /home/voxprofit  
# Set permissions to restrict access to user's home folder.

sudo chown -R voxprofit:voxprofit /home/voxprofit  
# Set ownership of all files in user's home directory.

reboot  
# Reboot the server to apply hostname and user group changes.

ssh -i ~/.ssh/id_rsa voxprofit@209.38.210.163   
# After reboot, reconnect using the "voxprofit" user.
```

## Github CI/CD Setup
```markdown
On Github, navigate to:
Settings > Secrets > Actions > New repository secret.

Local machine:
cat ~/.ssh/do_rsa | pbcopy
# Copy private SSH key for deployments.

- Paste the copied key into "Value" field on GitHub secrets page.
- Give this key a name e.g. DEPLOY_KEY and set additional secret variables as needed.
- Add self-hosted GitHub runner under settings > actions > runners following GitHub's on-screen instructions.

If you encounter permission errors during runner setup, run:
sudo chown -R yankz:yankz /var/www/actions-runner  
sudo chmod -R 755 /var/www/actions-runner  
sudo chown -R yankz:www-data /var/www/actions-runner  
sudo chmod -R 777 /var/www/actions-runner

To properly install the runner as a service use:
sudo ./svc.sh install  
sudo ./svc.sh start  
sudo ./svc.sh status  
sudo ./svc.sh stop
```

## Configure Nginx for Github Actions
```bash
sudo nano /etc/nginx/sites-available/default  
# Edit the default Nginx configuration file (follow linked guide in original instructions).

touch ~/.ssh/known_hosts  
ssh-keyscan github.com >> ~/.ssh/known_hosts  
# Avoid SSH key verification errors when accessing GitHub.

If verification errors persist, try:
ssh-keyscan -t rsa github.com >> ~/.ssh/known_hosts
```

## SSH Configuration Hardening
```bash
sudo nano /etc/ssh/sshd_config  
# Edit ssh server config to include these:
Port 2222  
PermitRootLogin no  
PasswordAuthentication no  
PubkeyAuthentication yes  
AuthorizedKeysFile .ssh/authorized_keys  
AllowUsers yankz  

sudo ufw allow 2222/tcp  
sudo systemctl restart sshd  
sudo ufw reload  
```

## GIT & GITHUB Integration
```bash
cd ~/.ssh && ssh-keygen  
# Generate new SSH key pair.

eval "$(ssh-agent -s)"  
# Start SSH agent to manage keys.

ssh-add ~/.ssh/id_rsa  
# Add private key to the SSH agent.

cat ~/.ssh/id_rsa.pub  
# Print public key, which you add to GitHub account to allow SSH cloning.

# auth with github
ssh -T git@github.com

sudo apt install git-all -y  
# Install complete Git suite.

git init
# Initialize a new git repository (usually not needed if cloning directly).

sudo git clone git@github.com:Yankzy/chatbot-backend.git  
# Clone your GitHub project into current directory.

echo "alias mk='make'" >> ~/.bashrc && source ~/.bashrc  
# Alias 'mk' command to 'make', for convenience.
```

## Django Commands:
```bash
python manage.py runserver 0.0.0.0:8000  
# Run Django development server.
```

## PostgreSQL Database Setup
```bash
sudo -i -u postgres psql  
# Login to PostgreSQL shell.

CREATE DATABASE fignode;  
ALTER USER postgres PASSWORD 'yourpassword';  
ALTER ROLE postgres SET client_encoding TO 'utf8';  
CREATE USER fignode WITH PASSWORD 'yourpassword';  
GRANT ALL PRIVILEGES ON DATABASE predictany TO postgres;

sudo nano /etc/postgresql/14/main/postgresql.conf  
# Set: listen_addresses = '*'

sudo nano /etc/postgresql/14/main/pg_hba.conf  
# Add line: host all all IP/32 md5

sudo systemctl restart postgresql@14-main  
sudo ufw allow 5432/tcp  
```

## React:
```bash
scp -i ~/.ssh/do_rsa -r build.zip root@IP:~/  
# Copy React build artifacts to your server remotely.
```

## Yarn/NPM (Node JS ecosystems):
```bash
yarn install && yarn build
npm install && npm run build
```

## Certbot (For Let's Encrypt SSL)
```bash
sudo apt-get install certbot python3-certbot-nginx -y
sudo certbot --nginx -d example.com -d www.example.com
```

## Supervisor (Process and Service Manager):
```bash
sudo apt-get install supervisor -y
sudo systemctl enable supervisor --now
```

## Daphne Config for Django Channels (WebSockets support):
```bash
sudo mkdir /run/daphne  
sudo chown root.root /run/daphne  
sudo nano /etc/supervisor/conf.d/daphne.conf
# Add provided Daphne program config in file
```

## Celery (Background Task Queue):
```ini
sudo nano /etc/supervisor/conf.d/celery.conf
# Add provided Celery configuration.
```

## Flower (Celery monitoring dashboard):
```ini
sudo nano /etc/supervisor/conf.d/flower.conf
# Add provided Flower configuration
```

## Redis server (Key/Value store for caching, Celery tasks):
```bash
sudo apt-get install redis-server -y  
sudo systemctl enable redis-server --now  
```

## Firewall UFW:
```bash
sudo apt-get install ufw  
sudo ufw default allow outgoing  
sudo ufw allow ssh  
sudo ufw allow 'Nginx Full'  
echo 'y' | sudo ufw enable  
```

## Miscellaneous (some permissions & ownership fixes):
```
chmod 700 ~/.ssh/  
chmod 600 ~/.ssh/*  

# Change file permissions for web directories as required
sudo chown -R yankz:www-data /your/directory
```

With these detailed explanations, each action you take during setup is clearly documented and understandable for future reference or debugging.