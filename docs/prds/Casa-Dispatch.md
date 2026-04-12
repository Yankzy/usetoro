# **UNIFIED TECHNICAL PRD: Toro OS "Casa-Dispatch" Architecture**

## **1. Product Vision & Operational Boundaries**
The system enforces strict friction boundaries based on user incentives.
* **The Rider Boundary (0 Friction):** 100% Headless. Riders use only WhatsApp. No app downloads. No passwords.
* **The Driver Boundary (High Persistence):** A lightweight React Native (RN) mobile application. It acts purely as a GPS sensor and a dispatch terminal. Authenticated entirely via WhatsApp OTP.
* **The Platform Boundary (The Brain):** The Toro OS Go/NATS monolith handles all state, the in-memory spatial index, and event routing. Python microservices are isolated entirely for stateless heavy-compute (OCR).

---

## **2. Core Data Models & Spatial Indexing**
**Ken Thompson:** We strictly divide persistent data (Disk) from ephemeral telemetry (RAM) to prevent database locking during high-velocity tracking.

### **2.1. Persistent Storage (PostgreSQL)**
* **Table: `drivers`**
    * `id` (UUID, Primary Key)
    * `whatsapp_number` (String, Unique)
    * `verification_status` (Enum: `PENDING`, `VERIFIED`, `BANNED`)
    * `taxi_plate_number` (String)
* **Table: `rides`**
    * `id` (UUID, Primary Key)
    * `rider_number` (String)
    * `assigned_driver_id` (UUID, Nullable until locked)
    * `status` (Enum: `SEARCHING`, `LOCKED`, `IN_TRANSIT`, `COMPLETED`, `CANCELLED`, `NO_SHOW`)

### **2.2. Ephemeral Storage (Go R-Tree In-Memory Index)**
* Uses an in-memory R-Tree (e.g., `github.com/tidwall/rtree`) protected by a `sync.RWMutex`.
* **Struct: `ActiveDriver`**
    * `DriverID` (UUID)
    * `Coordinates` (Lat/Long Float64 array)
    * `AvailableSeats` (Int - Crucial for Red Taxis, defaults to 3)
    * `LastPing` (Unix Timestamp)

---

## **3. The NATS Subject Topology (Event Routing)**
All inter-service communication happens here. 
* `ingress.whatsapp.rider_pin`: Fired when Twilio receives a location pin.
* `taxi.location.ping`: Fired every 5 seconds by the Driver RN app via WebSocket/gRPC.
* `taxi.dispatch.find_nearest`: Orchestrator asks Spatial Agent for candidates.
* `taxi.dispatch.broadcast`: Orchestrator pushes ride offers to the RN apps.
* `taxi.dispatch.accept`: Fired when a driver taps the RN "Accept" button.
* `task.verify.driver`: Fired to trigger the Python OCR agent for onboarding.

---

## **4. State Machine Lifecycles (The Exact Workflows)**

### **Workflow A: Driver Identity & Onboarding**
1.  **Auth:** Driver enters phone number in RN app. Go sends a 6-digit OTP via Twilio to their WhatsApp. Driver inputs OTP. Go issues a JWT.
2.  **Upload:** RN app prompts for *Permis de Confiance* photo. Base64 payload is sent to Go API -> NATS `task.verify.driver`.
3.  **Python OCR:** `Agent_Universal_OCR` picks it up, extracts `license_number` and `expiration_date`. Returns JSON to Go.
4.  **Resolution:** Go updates PostgreSQL `verification_status = VERIFIED`. Unlocks the RN app dashboard via WebSocket.

### **Workflow B: The Core Dispatch Race Condition**
1.  **The Request:** Rider sends WhatsApp Location Pin. 
2.  **The Spatial Query:** Go Orchestrator publishes `taxi.dispatch.find_nearest` with the Lat/Long and `radius_km: 2.0`. 
3.  **Candidate Selection:** The Go Spatial Agent queries the R-Tree, filtering out `AvailableSeats == 0` or `LastPing > 3 mins`. Returns the top 5 `DriverIDs`.
4.  **The Broadcast (FCM):** Orchestrator uses Firebase Cloud Messaging (FCM) to push a silent data payload to the 5 RN apps. 
5.  **RN Display & Lock:** A massive `[ACCEPT RIDE]` button appears. Driver B taps it first. RN fires `POST /api/v1/dispatch/accept`.
6.  **The Resolution:** * Go PostgreSQL transaction locks the ride to Driver B. 
    * Go Spatial Agent decrements Driver B's `AvailableSeats` by 1.
    * Go triggers Twilio: *"Driver B is 1.2km away. Estimated arrival: 4 minutes. Call: 06..."* (ETA calculated via fast Haversine distance).
    * Go sends an FCM silent push to dismiss the UI on the other 4 drivers.

---

## **5. Adversarial Defenses & Fault Tolerance**
You cannot run a ride-hailing network without automated, ruthless defense mechanisms against spoofing and ghosting.

* **The Mach 4 Trap (GPS Spoofing):** Before the Go Agent updates the R-Tree, it calculates the Delta-V. If `(Distance / Time) > 150 km/h`, the ping is dropped and the driver is shadowbanned for spoofing.
* **The Reaper Cron (Tunnel Drop):** A background Goroutine sweeps the R-Tree every 30 seconds. Any `ActiveDriver` with a `LastPing > 3 minutes` is purged from memory. This prevents riders from being assigned to taxis stuck in underpasses or with dead batteries.
* **The VoIP Firewall:** Twilio Lookup API blocks any virtual or non-Moroccan numbers from requesting a ride.
* **The Strike Protocol:** If a driver hits `[No Show]` at the physical pickup pin, the Rider gets a strike in Postgres. Two strikes = permanent ban.

---

## **6. API Contracts (Go <-> React Native)**
The boundary schema must be absolute. 

**1. Telemetry Stream (RN -> Go)**
```json
{
  "event": "location_update",
  "data": {
    "jwt_token": "eyJhbG...",
    "lat": 33.589886,
    "lng": -7.603869,
    "seats_available": 2,
    "accuracy_meters": 4.2
  }
}
```

**2. Dispatch Push (Go -> RN via FCM)**
```json
{
  "type": "NEW_RIDE_OFFER",
  "ride_id": "uuid-9928-111",
  "pickup_lat": 33.591000,
  "pickup_lng": -7.601000,
  "estimated_distance_km": 1.2
}
```

---

## **7. Infrastructure & Scalability**
* **Go Control Plane:** Single binary running on AWS `c6g.large` (ARM). Houses the Orchestrator, NATS JetStream, and the R-Tree memory index. 
* **Database:** Managed PostgreSQL for durable state (Auth, Strike counts, Ride history).
* **Python Workers:** AWS Lambda / GCP Cloud Run. Purely serverless. They execute the Gemini Plus OCR and spin down to 0 immediately. 
* **React Native App:** Built in Expo, using `expo-location` for persistent background tracking. 
