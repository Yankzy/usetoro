# **PRD: Toro OS Go-Native Geospatial Dispatcher (The "Red Taxi" Engine)**

Architecturally, this is no longer a stateless worker. This specific Go Agent is a **Stateful Memory Index**. It listens to the NATS firehose of incoming driver locations, updates an in-memory map using a Read-Write Mutex (`sync.RWMutex`), and instantly returns the 5 nearest drivers when the Orchestrator asks for them. 

#### **Phase 1: The Core Data Structure (The In-Memory Spatial Index)**
To do this in Go without a database, you will use **Geohashing** or an **In-Memory R-Tree** (like the `github.com/tidwall/rtree` library, which is an industry standard for Go spatial indexing).
* **The Entity:** `Active_Driver`
    * `driver_id` (String)
    * `current_lat`, `current_long` (Float64)
    * `available_seats` (Int - Crucial for Red Taxis)
    * `last_ping_timestamp` (Unix Time)
* **The Index:** An R-Tree tree structure held entirely in Go's RAM. Every time a driver's WhatsApp live location pings, Go updates their point in the tree. Because it is in memory, a search for "drivers within 2km" takes microseconds.

#### **Phase 2: Almanac Registration & NATS Subjects**
Even though this runs in Go, it must remain strictly decoupled from the main Orchestrator. It registers as a specialized Agent.
* **Almanac Registration:** `capabilities: ["spatial_index", "radius_search", "driver_routing"]`
* **Ingress Subject 1 (Location Updates):** `taxi.location.ping`
    * *Payload:* `{"driver_id": "taxi_88", "lat": 33.5898, "long": -7.6038, "seats": 2}`
    * *Action:* Go Agent updates the R-Tree. (No Ack required, it's a fire-and-forget telemetry stream).
* **Ingress Subject 2 (Dispatch Request):** `taxi.dispatch.find_nearest`
    * *Payload:* `{"rider_id": "user_123", "lat": 33.5900, "long": -7.6000, "radius_km": 2.0, "seats_needed": 1}`
    * *Action:* Go Agent queries the R-Tree, filters out stale drivers or full taxis, and replies with the array of candidate drivers.

#### **Phase 3: The State Machine Lifecycle (The Race Condition)**
This is where the physics of the physical world meet your code. You have to account for latency and human reaction times. 

1. **The Request:** Rider sends location pin. Orchestrator asks the Geospatial Agent for the 5 closest taxis. 
2. **The Broadcast:** Geospatial Agent returns `[Driver A, Driver B, Driver C]`. The Orchestrator blasts a Twilio/WhatsApp interactive message to all three simultaneously: *"Rider at Maarif Twin Center. Press 1 to Accept."*
3. **The Lock (Deterministic Winning):** Driver B presses "1" first. NATS receives the `taxi.dispatch.accepted` event with Driver B's ID. 
4. **The Cleanup:** The Orchestrator immediately locks the ride to Driver B. It then sends a "Ride no longer available" update to Driver A and Driver C, and tells the Geospatial Agent to decrement Driver B's `available_seats` in the spatial index.

#### **Phase 4: Fallbacks & The "Ghost Car" Problem**
Red Taxi drivers will go through tunnels. Their 4G will drop. Their battery will die. If your in-memory map thinks a driver is available but they dropped offline 15 minutes ago, you will give the rider a phantom ETA. 

* **The Reaper Cron (Background Goroutine):** You must write a lightweight Goroutine inside this Agent that ticks every 30 seconds. It sweeps the R-Tree for any `Active_Driver` whose `last_ping_timestamp` is older than 3 minutes.
* **The Action:** If a driver is stale, the Reaper completely purges them from the spatial index. They do not exist to the Orchestrator again until their WhatsApp connection sends a fresh location ping.

#### **Phase 5: Seamless UX Integration**
Because this spatial engine is so fast, the UX feels like magic to the user.
* When the rider drops the location pin, the Go Spatial Agent is so fast that the Orchestrator can reply in the *same second*: *"Found 14 taxis in your area. Requesting the closest one now..."*
* Once locked, the Orchestrator uses the Spatial Agent to calculate a quick Haversine distance (straight-line math) to give a rough ETA: *"Driver is 1.2km away. Estimated arrival: 4 minutes."*

***

By keeping the LLMs handling the intent, the Python workers handling the OCR, and using this pure-Go R-Tree Agent to handle the massive load of live map data, you are using every tool exactly for what it was built for. This architecture could run the entire dispatch system for Casablanca on a single, well-provisioned $40/month server. 

Since handling live location streams via WhatsApp requires persistent webhook updates, how are you planning to manage the authentication or session state for the drivers to ensure a bad actor doesn't spoof coordinates and send your taxis to the wrong side of the city?