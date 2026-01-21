This is the **Operator's Manual** for building the "Sovereign Triangle."

I have written this so you can execute it line-by-line. No guessing. We are building a **Geo-Redundant Banking Cluster** using your two MacBooks.

### The Mission

We are simulating three Data Centers:

1. **Casablanca (Primary):** Hosted on Mac A.
2. **Rabat (Secondary):** Hosted on Mac A (different port).
3. **Remote Witness (Tie-Breaker):** Hosted on Mac B.

---

### Phase 1: The Network Backbone (Do this first)

In the real world, you buy a domain (e.g., `usetoro.io`) and set up VPNs. We will simulate this using **Split-Horizon DNS**. This tricks your computers into thinking `casa.usetoro.io` is a real server.

#### Step 1.1: Find your Real IP Addresses

You need the actual Local IP of both Macs.

* **On Mac A (Main):** Open Terminal -> Run `ipconfig getifaddr en0` (or `en1` for Wi-Fi).
* *I will assume it is `192.168.1.10` for this guide.*


* **On Mac B (Witness):** Run `ipconfig getifaddr en0`.
* *I will assume it is `192.168.1.20` for this guide.*



**⚠️ CRITICAL:** Wherever you see `192.168.1.10` or `192.168.1.20` below, **replace them with your actual IPs.**

#### Step 1.2: Configure DNS on Mac A

Run this command to open your hosts file:

```bash
sudo nano /etc/hosts

```

**Paste this at the bottom:**

```text
# --- BANKING INFRASTRUCTURE ---
127.0.0.1       casa.usetoro.io
127.0.0.1       rabat.usetoro.io
192.168.1.20    witness.usetoro.io

```

*(Press `Ctrl+O` then `Enter` to save, `Ctrl+X` to exit)*

#### Step 1.3: Configure DNS on Mac B

Run `sudo nano /etc/hosts` and paste:

```text
# --- BANKING INFRASTRUCTURE ---
192.168.1.10    casa.usetoro.io
192.168.1.10    rabat.usetoro.io
127.0.0.1       witness.usetoro.io

```

**Why do we do this?**
If you ever move your servers, you only update this text file. You never have to touch your application code or server configs again.

---

### Phase 2: Deploy Casablanca & Rabat (On Mac A)

We are treating these as two separate projects to simulate physical separation.

#### Step 2.1: Create Project Structures

Run these commands to create the folders:

```bash
mkdir -p ~/sovereign-bank/dc-casablanca/storage
mkdir -p ~/sovereign-bank/dc-rabat/storage
cd ~/sovereign-bank

```

#### Step 2.2: Configure CASABLANCA (The Leader)

Create the config file.
`nano dc-casablanca/nats.conf`

**Paste this:**

```text
server_name: "CASABLANCA"
port: 4222
http_port: 8222

jetstream {
    store_dir: "/data/storage"
}

cluster {
    name: "USETORO_BANK"
    port: 6222
    
    # CRITICAL: This tells the cluster "I am reachable here"
    advertise: "casa.usetoro.io:6222"

    routes = [
        "nats://rabat.usetoro.io:6223",
        "nats://witness.usetoro.io:6222"
    ]
}

```

Create the Docker file.
`nano dc-casablanca/docker-compose.yaml`

**Paste this:**

```yaml
version: "3.9"
services:
  nats:
    image: nats:latest
    container_name: nats-casablanca
    restart: always
    volumes:
      - ./nats.conf:/nats.conf
      - ./storage:/data/storage
    command: "-c /nats.conf"
    ports:
      - "4222:4222"
      - "6222:6222"
      - "8222:8222"
    # This injects our "Fake DNS" into the container
    extra_hosts:
      - "casa.usetoro.io:192.168.1.10"
      - "rabat.usetoro.io:192.168.1.10"
      - "witness.usetoro.io:192.168.1.20"

```

#### Step 2.3: Configure RABAT (The Follower)

Create the config.
`nano dc-rabat/nats.conf`

**Paste this:**

```text
server_name: "RABAT"
port: 4222
http_port: 8222

jetstream {
    store_dir: "/data/storage"
}

cluster {
    name: "USETORO_BANK"
    port: 6223
    
    advertise: "rabat.usetoro.io:6223"

    routes = [
        "nats://casa.usetoro.io:6222",
        "nats://witness.usetoro.io:6222"
    ]
}

```

Create the Docker file.
`nano dc-rabat/docker-compose.yaml`

**Paste this:**

```yaml
version: "3.9"
services:
  nats:
    image: nats:latest
    container_name: nats-rabat
    restart: always
    volumes:
      - ./nats.conf:/nats.conf
      - ./storage:/data/storage
    command: "-c /nats.conf"
    ports:
      - "4223:4222" # Note: Client connects on 4223
      - "6223:6223" # Cluster connects on 6223
      - "8223:8222"
    extra_hosts:
      - "casa.usetoro.io:192.168.1.10"
      - "rabat.usetoro.io:192.168.1.10"
      - "witness.usetoro.io:192.168.1.20"

```

---

### Phase 3: Deploy Witness (On Mac B)

Switch to your second computer.

#### Step 3.1: Create Structure

```bash
mkdir -p ~/sovereign-bank/dc-witness/storage
cd ~/sovereign-bank

```

#### Step 3.2: Configure WITNESS

`nano dc-witness/nats.conf`

**Paste this:**

```text
server_name: "WITNESS"
port: 4222
http_port: 8222

jetstream {
    store_dir: "/data/storage"
}

cluster {
    name: "USETORO_BANK"
    port: 6222
    
    advertise: "witness.usetoro.io:6222"

    routes = [
        "nats://casa.usetoro.io:6222",
        "nats://rabat.usetoro.io:6223"
    ]
}

```

`nano dc-witness/docker-compose.yaml`

**Paste this:**

```yaml
version: "3.9"
services:
  nats:
    image: nats:latest
    container_name: nats-witness
    restart: always
    volumes:
      - ./nats.conf:/nats.conf
      - ./storage:/data/storage
    command: "-c /nats.conf"
    ports:
      - "4222:4222"
      - "6222:6222"
      - "8222:8222"
    extra_hosts:
      - "casa.usetoro.io:192.168.1.10"
      - "rabat.usetoro.io:192.168.1.10"
      - "witness.usetoro.io:192.168.1.20"

```

---

### Phase 4: Ignition Sequence

We need to start them in a specific order to watch the cluster form.

**1. Start Casablanca (Mac A):**

```bash
cd ~/sovereign-bank/dc-casablanca
docker-compose up -d

```

**2. Start Witness (Mac B):**

```bash
cd ~/sovereign-bank/dc-witness
docker-compose up -d

```

*Wait 10 seconds. Witness will try to find Casablanca.*

**3. Start Rabat (Mac A):**

```bash
cd ~/sovereign-bank/dc-rabat
docker-compose up -d

```

**4. Verify Health:**
Open your browser on Mac A and go to: `http://casa.usetoro.io:8222/jsz?server=1`
Look for **"replicas"**. You should see a list of 3 servers. If you see this, you have successfully built a distributed system.

---

### Phase 5: The "Bank Grade" Setup (Replication)

Now we must tell the cluster: *"Any message sent to `bank.transactions` MUST be written to 3 hard drives."*

You need the `nats` CLI tool installed on Mac A (`brew install nats-io/nats-tools/nats`), OR we can run this via Docker:

**Run this command on Mac A:**

```bash
docker run --network host --rm -it natsio/nats-box \
 nats stream add BANK_TRANSACTIONS \
 --server="nats://127.0.0.1:4222" \
 --subjects="bank.transactions.*" \
 --storage=file \
 --replicas=3 \
 --retention=work \
 --discard=new \
 --ack=explicit

```

**What did you just do?**

* `--replicas=3`: This is the contract. If a message isn't on 3 disks, it's not "saved."
* `--storage=file`: Don't use RAM. Use the Disk.

---

### Phase 6: The Disaster Simulation (The Fun Part)

**Step 1: Put money in the bank.**
Publish a message using the cluster leader (Casablanca).

```bash
docker run --network host --rm -it natsio/nats-box \
 nats pub bank.transactions.deposit '{"amount": 5000}' \
 --server="nats://127.0.0.1:4222"

```

*Response: Published 17 bytes to "bank.transactions.deposit"*

**Step 2: NUKE Casablanca.**
We simulate a catastrophic data center failure.

```bash
cd ~/sovereign-bank/dc-casablanca
docker-compose down

```

*Casablanca is now dead.*

**Step 3: Check Rabat.**
Can we still find the money? We query the secondary node (Rabat runs on port 4223).

```bash
docker run --network host --rm -it natsio/nats-box \
 nats stream view BANK_TRANSACTIONS \
 --server="nats://127.0.0.1:4223"

```

**Result:**
You will see `{"amount": 5000}`.
The system survived. You are ready for the Central Bank audit.