const int NUM_SLIDERS = 5;
const int analogInputs[NUM_SLIDERS] = {A0, A1, A2, A3, A4};
const String FIRMWARE_VERSION = "v1.0";

int analogSliderValues[NUM_SLIDERS];
unsigned long lastHeartbeat = 0;
const unsigned long HEARTBEAT_INTERVAL = 5000; // 5 seconds

void setup() { 
  for (int i = 0; i < NUM_SLIDERS; i++) {
    pinMode(analogInputs[i], INPUT);
  }

  Serial.begin(9600);
  delay(1000);

  // Send startup signal with version and capabilities
  Serial.println("deej:" + FIRMWARE_VERSION + ":5sliders");
}

void loop() {
  updateSliderValues();
  sendSliderValues();
  
  // Send heartbeat every 5 seconds
  if (millis() - lastHeartbeat > HEARTBEAT_INTERVAL) {
    Serial.println("heartbeat");
    lastHeartbeat = millis();
  }
  
  // Send status report every 10 seconds
  static unsigned long lastStatus = 0;
  if (millis() - lastStatus > 10000) {
    sendStatusReport();
    lastStatus = millis();
  }
  
  delay(10);
}

void updateSliderValues() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
     analogSliderValues[i] = analogRead(analogInputs[i]);
  }
}

void sendSliderValues() {
  String builtString = String("");

  for (int i = 0; i < NUM_SLIDERS; i++) {
    builtString += String((int)analogSliderValues[i]);

    if (i < NUM_SLIDERS - 1) {
      builtString += String("|");
    }
  }
  
  Serial.println(builtString);
}

void sendStatusReport() {
  // Check if all sliders are responding (not stuck at 0 or 1023)
  bool allSlidersWorking = true;
  for (int i = 0; i < NUM_SLIDERS; i++) {
    if (analogSliderValues[i] == 0 || analogSliderValues[i] == 1023) {
      allSlidersWorking = false;
      break;
    }
  }
  
  if (allSlidersWorking) {
    Serial.println("status:ok");
  } else {
    Serial.println("status:warning");
  }
}

void printSliderValues() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    String printedString = String("Slider #") + String(i + 1) + String(": ") + String(analogSliderValues[i]) + String(" mV");
    Serial.write(printedString.c_str());

    if (i < NUM_SLIDERS - 1) {
      Serial.write(" | ");
    } else {
      Serial.write("\n");
    }
  }
}
