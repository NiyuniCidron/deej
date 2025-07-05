/*
 * deej Arduino Firmware
 * Version: v2.0
 * Description: 5-slider volume control for deej
 */

const int NUM_SLIDERS = 5;
const int analogInputs[NUM_SLIDERS] = {A0, A1, A2, A3, A4};
const char* FIRMWARE_VERSION = "v2.0";

int analogSliderValues[NUM_SLIDERS];
int lastSentValues[NUM_SLIDERS];
const int CHANGE_THRESHOLD = 10; // Only send if value changes by more than this amount

void setup() { 
  for (int i = 0; i < NUM_SLIDERS; i++) {
    pinMode(analogInputs[i], INPUT);
  }

  Serial.begin(9600);
  delay(1000);

  // Send startup signal with version and capabilities
  Serial.print("deej:");
  Serial.print(FIRMWARE_VERSION);
  Serial.println(":startup:5sliders");
  
  // Initialize last sent values to force initial send
  for (int i = 0; i < NUM_SLIDERS; i++) {
    lastSentValues[i] = -1; // Force initial send
  }
}

void loop() {
  updateSliderValues();
  
  // Prioritize slider data - send immediately if there are changes
  if (hasSignificantChanges()) {
    sendSliderValues();
    updateLastSentValues();
  }
  
  // Reduced delay for better responsiveness
  delay(20); // Increased from 5ms to 20ms to reduce message frequency
}

void updateSliderValues() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
     analogSliderValues[i] = analogRead(analogInputs[i]);
  }
}

bool hasSignificantChanges() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    if (abs(analogSliderValues[i] - lastSentValues[i]) > CHANGE_THRESHOLD) {
      return true;
    }
  }
  return false;
}

void updateLastSentValues() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    lastSentValues[i] = analogSliderValues[i];
  }
}

void sendSliderValues() {
  // Use more efficient string building
  Serial.print("deej:");
  Serial.print(FIRMWARE_VERSION);
  Serial.print(":sliders:");
  
  for (int i = 0; i < NUM_SLIDERS; i++) {
    Serial.print(analogSliderValues[i]);
    if (i < NUM_SLIDERS - 1) {
      Serial.print("|");
    }
  }
  Serial.println();
}

void checkForCommands() {
  if (Serial.available()) {
    String incoming = Serial.readStringUntil('\n');
    incoming.trim();
    
    // Check if this is a command message
    if (incoming.startsWith("deej:")) {
      // Parse the command
      int firstColon = incoming.indexOf(':', 5); // Skip "deej:"
      if (firstColon != -1) {
        int secondColon = incoming.indexOf(':', firstColon + 1);
        if (secondColon != -1) {
          String messageType = incoming.substring(firstColon + 1, secondColon);
          if (messageType == "command") {
            String command = incoming.substring(secondColon + 1);
            processCommand(command);
          }
        }
      }
    }
  }
}

void processCommand(String command) {
  command.trim();
  
  if (command == "reboot") {
    // Send acknowledgment before rebooting
    Serial.print("deej:");
    Serial.print(FIRMWARE_VERSION);
    Serial.println(":response:reboot_ack");
    delay(100); // Give time for the response to be sent
    // Soft reboot by jumping to address 0
    asm volatile ("jmp 0");
  }
  else if (command == "version") {
    Serial.print("deej:");
    Serial.print(FIRMWARE_VERSION);
    Serial.print(":response:version:");
    Serial.println(FIRMWARE_VERSION);
  }
  else if (command == "sliders") {
    sendSliderValues();
  }
  else {
    // Unknown command
    Serial.print("deej:");
    Serial.print(FIRMWARE_VERSION);
    Serial.print(":response:error:unknown_command:");
    Serial.println(command);
  }
}
