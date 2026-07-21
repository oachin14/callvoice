-- CallVoice inbound: fire CUSTOM callvoice::inbound then park for edge uuid_transfer/uuid_kill.
-- Loaded from dialplan public context when destination matches a DID-like number.
local did = session:getVariable("destination_number") or ""
session:setVariable("callvoice_did", did)
session:execute("event", "Event-Subclass=callvoice::inbound,Event-Name=CUSTOM,callvoice_did=" .. did)
session:execute("park")
