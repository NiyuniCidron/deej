package deej

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// WebConfigServer provides a web-based configuration interface
type WebConfigServer struct {
	logger *zap.SugaredLogger
	deej   *Deej
	config *CanonicalConfig
	server *http.Server
}

// ConfigData represents the configuration data for the web interface
type ConfigData struct {
	SliderMappings map[string]string `json:"sliderMappings"`
	InvertSliders  bool              `json:"invertSliders"`
	COMPort        string            `json:"comPort"`
	BaudRate       int               `json:"baudRate"`
	NoiseReduction string            `json:"noiseReduction"`
	NumSliders     int               `json:"numSliders"`
}

// NewWebConfigServer creates a new web configuration server
func NewWebConfigServer(deej *Deej, logger *zap.SugaredLogger) *WebConfigServer {
	logger = logger.Named("web_config")

	wcs := &WebConfigServer{
		logger: logger,
		deej:   deej,
		config: deej.config,
	}

	// Set up HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/", wcs.handleIndex)
	mux.HandleFunc("/api/config", wcs.handleGetConfig)
	mux.HandleFunc("/api/save", wcs.handleSaveConfig)
	mux.HandleFunc("/api/targets", wcs.handleGetTargets)

	wcs.server = &http.Server{
		Addr:    "localhost:8080",
		Handler: mux,
	}

	return wcs
}

// Start starts the web configuration server
func (wcs *WebConfigServer) Start() error {
	wcs.logger.Info("Starting web configuration server on http://localhost:8080")
	return wcs.server.ListenAndServe()
}

// Stop stops the web configuration server
func (wcs *WebConfigServer) Stop() error {
	wcs.logger.Info("Stopping web configuration server")
	return wcs.server.Close()
}

// handleIndex serves the main configuration page
func (wcs *WebConfigServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// HTML template for the configuration page
	htmlTemplate := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>deej Configuration</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            max-width: 800px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background: white;
            padding: 30px;
            border-radius: 8px;
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
        }
        h1 {
            color: #333;
            margin-bottom: 30px;
            text-align: center;
        }
        .section {
            margin-bottom: 30px;
            padding: 20px;
            border: 1px solid #e0e0e0;
            border-radius: 5px;
        }
        .section h2 {
            color: #555;
            margin-top: 0;
            border-bottom: 2px solid #007acc;
            padding-bottom: 10px;
        }
        .form-group {
            margin-bottom: 15px;
        }
        label {
            display: block;
            margin-bottom: 5px;
            font-weight: 500;
            color: #333;
        }
        input[type="text"], input[type="number"], select {
            width: 100%;
            padding: 8px 12px;
            border: 1px solid #ddd;
            border-radius: 4px;
            font-size: 14px;
            box-sizing: border-box;
        }
        input[type="checkbox"] {
            margin-right: 8px;
        }
        .slider-row {
            display: flex;
            align-items: center;
            margin-bottom: 10px;
        }
        .slider-row label {
            min-width: 80px;
            margin-bottom: 0;
            margin-right: 10px;
        }
        .slider-row input {
            flex: 1;
        }
        .special-btn {
            background: #007acc;
            color: white;
            border: none;
            padding: 6px 12px;
            border-radius: 4px;
            cursor: pointer;
            margin-left: 10px;
            font-size: 12px;
        }
        .special-btn:hover {
            background: #005a9e;
        }
        .buttons {
            text-align: center;
            margin-top: 30px;
        }
        .btn {
            padding: 12px 24px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 16px;
            margin: 0 10px;
        }
        .btn-primary {
            background: #007acc;
            color: white;
        }
        .btn-primary:hover {
            background: #005a9e;
        }
        .btn-secondary {
            background: #6c757d;
            color: white;
        }
        .btn-secondary:hover {
            background: #545b62;
        }
        .help-text {
            color: #666;
            font-size: 14px;
            margin-bottom: 15px;
        }
        .modal {
            display: none;
            position: fixed;
            z-index: 1000;
            left: 0;
            top: 0;
            width: 100%;
            height: 100%;
            background-color: rgba(0,0,0,0.5);
        }
        .modal-content {
            background-color: white;
            margin: 5% auto;
            padding: 20px;
            border-radius: 8px;
            width: 90%;
            max-width: 600px;
            max-height: 80vh;
            overflow-y: auto;
        }
        .modal-buttons {
            text-align: center;
            margin-top: 20px;
        }
        .modal-btn {
            margin: 0 5px;
            padding: 8px 16px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
        }
        .success-message {
            background: #d4edda;
            color: #155724;
            padding: 10px;
            border-radius: 4px;
            margin-bottom: 20px;
            display: none;
        }
        .error-message {
            background: #f8d7da;
            color: #721c24;
            padding: 10px;
            border-radius: 4px;
            margin-bottom: 20px;
            display: none;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>deej Configuration</h1>
        
        <div id="successMessage" class="success-message"></div>
        <div id="errorMessage" class="error-message"></div>
        
        <form id="configForm">
            <div class="section">
                <h2>Slider Mappings</h2>
                <div style="text-align: right; margin-bottom: 10px;">
                    <button type="button" class="btn btn-secondary" onclick="refreshSliderCount()" style="padding: 6px 12px; font-size: 12px;">Refresh Slider Count</button>
                </div>
                <div id="sliderMappings">
                    <!-- Slider mappings will be populated by JavaScript -->
                </div>
            </div>
            
            <details style="margin-bottom: 30px;">
                <summary style="font-size: 1.1em; font-weight: bold;">Advanced</summary>
                <div class="section" style="margin-top: 15px;">
                    <h2>Connection Settings</h2>
                    <div class="form-group">
                        <label for="comPort">COM Port:</label>
                        <input type="text" id="comPort" name="comPort" placeholder="e.g., COM4 or auto">
                    </div>
                    <div class="form-group">
                        <label for="baudRate">Baud Rate:</label>
                        <input type="number" id="baudRate" name="baudRate" value="9600">
                    </div>
                </div>
            </details>
            
            <div class="section">
                <h2>Other Settings</h2>
                <div class="form-group">
                    <label>
                        <input type="checkbox" id="invertSliders" name="invertSliders">
                        Invert sliders
                    </label>
                </div>
                <div class="form-group">
                    <label for="noiseReduction">Noise Reduction:</label>
                    <select id="noiseReduction" name="noiseReduction">
                        <option value="low">Low (excellent hardware)</option>
                        <option value="default" selected>Default (regular hardware)</option>
                        <option value="high">High (bad, noisy hardware)</option>
                    </select>
                </div>
            </div>
            
            <div class="buttons">
                <button type="button" class="btn btn-secondary" onclick="window.close()">Cancel</button>
                <button type="submit" class="btn btn-primary">Save Configuration</button>
            </div>
        </form>
    </div>
    
    <!-- Audio targets modal -->
    <div id="specialModal" class="modal">
        <div class="modal-content">
            <h3>Select Audio Target</h3>
            <div id="specialTargetsSearchContainer"></div>
            <div style="text-align:right; margin-bottom:8px;">
                <button id="rescanRunningBtn" class="btn btn-secondary" style="padding:6px 12px; font-size:12px;">Rescan Running Applications</button>
            </div>
            <div id="specialTargetsList"></div>
            <div class="modal-buttons">
                <button class="modal-btn btn-secondary" onclick="closeSpecialModal()">Cancel</button>
            </div>
        </div>
    </div>
    
    <script>
        let currentSliderIndex = 0;
        
        // Load configuration on page load
        window.onload = function() {
            loadConfig();
        };
        
        function loadConfig() {
            fetch('/api/config')
                .then(response => response.json())
                .then(data => {
                    populateSliderMappings(data.sliderMappings, data.numSliders);
                    document.getElementById('comPort').value = data.comPort;
                    document.getElementById('baudRate').value = data.baudRate;
                    document.getElementById('invertSliders').checked = data.invertSliders;
                    document.getElementById('noiseReduction').value = data.noiseReduction;
                })
                .catch(error => {
                    showError('Failed to load configuration: ' + error.message);
                });
        }
        
        function refreshSliderCount() {
            fetch('/api/config')
                .then(response => response.json())
                .then(data => {
                    populateSliderMappings(data.sliderMappings, data.numSliders);
                    showSuccess('Slider count refreshed: ' + data.numSliders + ' slider(s) detected');
                })
                .catch(error => {
                    showError('Failed to refresh slider count: ' + error.message);
                });
        }
        
        function populateSliderMappings(mappings, numSliders) {
            const container = document.getElementById('sliderMappings');
            container.innerHTML = '';
            
            // Add slider count info
            const infoDiv = document.createElement('div');
            infoDiv.className = 'help-text';
            if (numSliders > 0) {
                infoDiv.innerHTML = '<strong>Detected ' + numSliders + ' slider(s) from Arduino</strong><br>Enter process names (e.g., chrome.exe) or special targets (master, mic, deej.unmapped, etc.)<br>Multiple targets can be separated by commas';
            } else {
                infoDiv.innerHTML = '<strong style="color: #dc3545;">Arduino not connected - using default 5 sliders</strong><br>Connect your Arduino and click "Refresh Slider Count" to detect the actual number of sliders<br>Enter process names (e.g., chrome.exe) or special targets (master, mic, deej.unmapped, etc.)<br>Multiple targets can be separated by commas';
            }
            container.appendChild(infoDiv);
            
            for (let i = 0; i < numSliders; i++) {
                const sliderDiv = document.createElement('div');
                sliderDiv.className = 'slider-row';
                
                const label = document.createElement('label');
                label.textContent = 'Slider ' + (i + 1) + ':';
                
                const input = document.createElement('input');
                input.type = 'text';
                input.name = 'slider' + i;
                input.placeholder = 'e.g., chrome.exe, firefox.exe';
                input.value = mappings[i] || '';
                
                const specialBtn = document.createElement('button');
                specialBtn.type = 'button';
                specialBtn.className = 'special-btn';
                specialBtn.textContent = 'Pick Target';
                specialBtn.onclick = function() { showSpecialModal(i); };
                
                sliderDiv.appendChild(label);
                sliderDiv.appendChild(input);
                sliderDiv.appendChild(specialBtn);
                container.appendChild(sliderDiv);
            }
        }
        
        function showSpecialModal(sliderIndex) {
            currentSliderIndex = sliderIndex;
            const modal = document.getElementById('specialModal');
            const list = document.getElementById('specialTargetsList');
            const searchContainer = document.getElementById('specialTargetsSearchContainer');
            list.innerHTML = '<div style="text-align: center; margin-bottom: 10px;"><strong>Loading available targets...</strong></div>';
            searchContainer.innerHTML = '<input type="text" id="specialTargetsSearch" placeholder="Search installed applications..." style="width: 100%; padding: 8px; margin-bottom: 10px; border-radius: 4px; border: 1px solid #ccc; font-size: 14px; display: block;">';
            modal.style.display = 'block';
            // Fetch available targets from the server
            fetch('/api/targets')
                .then(response => response.json())
                .then(targets => {
                    window._allAudioTargets = targets;
                    renderSpecialTargets(targets, '');
                    document.getElementById('specialTargetsSearch').oninput = function(e) {
                        renderSpecialTargets(window._allAudioTargets, e.target.value);
                    };
                    // Add rescan button handler
                    document.getElementById('rescanRunningBtn').onclick = function() {
                        list.innerHTML = '<div style="text-align: center; margin-bottom: 10px;"><strong>Rescanning running applications...</strong></div>';
                        fetch('/api/targets?refresh=1')
                            .then(response => response.json())
                            .then(targets => {
                                window._allAudioTargets = targets;
                                renderSpecialTargets(targets, document.getElementById('specialTargetsSearch').value);
                            });
                    };
                })
                .catch(error => {
                    list.innerHTML = '<div style="text-align: center; color: #dc3545;">Failed to load targets: ' + error.message + '</div>';
                });
        }
        
        function renderSpecialTargets(targets, search) {
            const list = document.getElementById('specialTargetsList');
            search = (search || '').toLowerCase();
            list.innerHTML = '';
            // Group targets by type and category
            const specialTargets = targets.filter(t => t.type === 'special');
            const processTargets = targets.filter(t => t.type === 'process');
            const mprisTargets = targets.filter(t => t.type === 'mpris');
            const deviceTargets = targets.filter(t => t.type === 'device');
            let installedTargets = targets.filter(t => t.type === 'installed');
            // Filter installed targets by search
            if (search) {
                installedTargets = installedTargets.filter(t =>
                    t.displayName.toLowerCase().includes(search) ||
                    (t.category && t.category.toLowerCase().includes(search))
                );
            }
            // Add special targets section
            if (specialTargets.length > 0) {
                const specialSection = document.createElement('div');
                specialSection.innerHTML = '<h4 style="margin: 10px 0 5px 0; color: #007acc;">System Controls</h4>';
                list.appendChild(specialSection);
                specialTargets.forEach(target => {
                    const btn = document.createElement('button');
                    btn.className = 'modal-btn btn-primary';
                    btn.textContent = target.displayName;
                    btn.title = target.description;
                    btn.onclick = function() { selectTarget(target.name); };
                    list.appendChild(btn);
                });
            }
            // Add process targets section
            if (processTargets.length > 0) {
                const processSection = document.createElement('div');
                processSection.innerHTML = '<h4 style="margin: 15px 0 5px 0; color: #007acc;">Running Applications</h4>';
                list.appendChild(processSection);
                processTargets.forEach(target => {
                    const btn = document.createElement('button');
                    btn.className = 'modal-btn btn-secondary';
                    btn.textContent = target.displayName;
                    btn.title = target.description;
                    btn.onclick = function() { selectTarget(target.name); };
                    list.appendChild(btn);
                });
            }
            // Add MPRIS media players section
            if (mprisTargets.length > 0) {
                const mprisSection = document.createElement('div');
                mprisSection.innerHTML = '<h4 style="margin: 15px 0 5px 0; color: #007acc;">Media Players</h4>';
                list.appendChild(mprisSection);
                mprisTargets.forEach(target => {
                    const btn = document.createElement('button');
                    btn.className = 'modal-btn btn-secondary';
                    btn.textContent = target.displayName;
                    btn.title = target.description;
                    btn.onclick = function() { selectTarget(target.name); };
                    mprisSection.appendChild(btn);
                });
            }
            // Add device targets section
            if (deviceTargets.length > 0) {
                const deviceSection = document.createElement('div');
                deviceSection.innerHTML = '<h4 style="margin: 15px 0 5px 0; color: #007acc;">Audio Devices</h4>';
                list.appendChild(deviceSection);
                deviceTargets.forEach(target => {
                    const btn = document.createElement('button');
                    btn.className = 'modal-btn btn-secondary';
                    btn.textContent = target.displayName;
                    btn.title = target.description;
                    btn.onclick = function() { selectTarget(target.name); };
                    list.appendChild(btn);
                });
            }
            // Add installed applications section (grouped by category)
            if (installedTargets.length > 0) {
                const installedSection = document.createElement('div');
                installedSection.innerHTML = '<h4 style="margin: 15px 0 5px 0; color: #007acc;">Installed Applications</h4>';
                list.appendChild(installedSection);
                // Group installed apps by category
                const categories = {};
                installedTargets.forEach(target => {
                    const category = target.category || 'Other';
                    if (!categories[category]) {
                        categories[category] = [];
                    }
                    categories[category].push(target);
                });
                // Sort categories alphabetically
                const sortedCategories = Object.keys(categories).sort();
                sortedCategories.forEach(category => {
                    const categorySection = document.createElement('div');
                    categorySection.style.marginLeft = '15px';
                    categorySection.style.marginBottom = '10px';
                    const categoryHeader = document.createElement('h5');
                    categoryHeader.textContent = category;
                    categoryHeader.style.margin = '10px 0 5px 0';
                    categoryHeader.style.color = '#666';
                    categoryHeader.style.fontSize = '14px';
                    categorySection.appendChild(categoryHeader);
                    // Sort apps within category alphabetically
                    categories[category].sort((a, b) => a.displayName.localeCompare(b.displayName));
                    categories[category].forEach(target => {
                        const btn = document.createElement('button');
                        btn.className = 'modal-btn btn-secondary';
                        btn.style.fontSize = '12px';
                        btn.style.padding = '6px 12px';
                        btn.style.margin = '2px 4px';
                        btn.textContent = target.displayName;
                        btn.title = target.description || target.displayName;
                        btn.onclick = function() { selectTarget(target.name); };
                        categorySection.appendChild(btn);
                    });
                    list.appendChild(categorySection);
                });
            }
            if (specialTargets.length === 0 && processTargets.length === 0 && deviceTargets.length === 0 && installedTargets.length === 0) {
                list.innerHTML = '<div style="text-align: center; color: #666;">No audio targets found</div>';
            }
        }
        
        function closeSpecialModal() {
            document.getElementById('specialModal').style.display = 'none';
        }
        
        function selectTarget(target) {
            const input = document.querySelector('input[name="slider' + currentSliderIndex + '"]');
            const currentValue = input.value;
            if (currentValue) {
                input.value = currentValue + ', ' + target;
            } else {
                input.value = target;
            }
            closeSpecialModal();
        }
        
        // Handle form submission
        document.getElementById('configForm').onsubmit = function(e) {
            e.preventDefault();
            
            const formData = {
                sliderMappings: {},
                comPort: document.getElementById('comPort').value,
                baudRate: parseInt(document.getElementById('baudRate').value),
                invertSliders: document.getElementById('invertSliders').checked,
                noiseReduction: document.getElementById('noiseReduction').value
            };
            
            // Collect slider mappings
            const numSliders = document.querySelectorAll('.slider-row').length;
            for (let i = 0; i < numSliders; i++) {
                const input = document.querySelector('input[name="slider' + i + '"]');
                if (input && input.value.trim()) {
                    formData.sliderMappings[i] = input.value.trim();
                }
            }
            
            // Send to server
            fetch('/api/save', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(formData)
            })
            .then(response => response.json())
            .then(data => {
                if (data.success) {
                    showSuccess('Configuration saved successfully!');
                } else {
                    showError('Failed to save configuration: ' + data.error);
                }
            })
            .catch(error => {
                showError('Failed to save configuration: ' + error.message);
            });
        };
        
        function showSuccess(message) {
            const successDiv = document.getElementById('successMessage');
            successDiv.textContent = message;
            successDiv.style.display = 'block';
            setTimeout(() => {
                successDiv.style.display = 'none';
            }, 5000);
        }
        
        function showError(message) {
            const errorDiv = document.getElementById('errorMessage');
            errorDiv.textContent = message;
            errorDiv.style.display = 'block';
            setTimeout(() => {
                errorDiv.style.display = 'none';
            }, 5000);
        }
        
        // Close modal when clicking outside
        window.onclick = function(event) {
            const modal = document.getElementById('specialModal');
            if (event.target === modal) {
                closeSpecialModal();
            }
        }
    </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(htmlTemplate))
}

// handleGetConfig returns the current configuration as JSON
func (wcs *WebConfigServer) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get the number of sliders from the Arduino connection
	numSliders := wcs.deej.serial.GetNumSliders()
	if numSliders == 0 {
		// If not connected, default to 5 sliders (most common)
		numSliders = 5
	}

	// Convert slider mappings to string format for the web interface
	sliderMappings := make(map[string]string)
	sliderMap := wcs.config.SliderMapping
	for i := 0; i < numSliders; i++ {
		if targets, exists := sliderMap.get(i); exists {
			sliderMappings[strconv.Itoa(i)] = strings.Join(targets, ", ")
		}
	}

	configData := ConfigData{
		SliderMappings: sliderMappings,
		InvertSliders:  wcs.config.InvertSliders,
		COMPort:        wcs.config.ConnectionInfo.COMPort,
		BaudRate:       wcs.config.ConnectionInfo.BaudRate,
		NoiseReduction: wcs.config.NoiseReductionLevel,
		NumSliders:     numSliders,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(configData)
}

// handleSaveConfig saves the configuration from the web interface
func (wcs *WebConfigServer) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requestData struct {
		SliderMappings map[string]string `json:"sliderMappings"`
		COMPort        string            `json:"comPort"`
		BaudRate       int               `json:"baudRate"`
		InvertSliders  bool              `json:"invertSliders"`
		NoiseReduction string            `json:"noiseReduction"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Convert slider mappings to the format expected by viper
	sliderMapping := make(map[string][]string)
	for sliderStr, targetsStr := range requestData.SliderMappings {
		if targetsStr != "" {
			targets := strings.Split(targetsStr, ",")
			var cleanTargets []string
			for _, target := range targets {
				target = strings.TrimSpace(target)
				if target != "" {
					cleanTargets = append(cleanTargets, target)
				}
			}
			if len(cleanTargets) > 0 {
				sliderMapping[sliderStr] = cleanTargets
			}
		}
	}

	// Update the viper config
	wcs.config.userConfig.Set("slider_mapping", sliderMapping)
	wcs.config.userConfig.Set("invert_sliders", requestData.InvertSliders)
	wcs.config.userConfig.Set("com_port", strings.TrimSpace(requestData.COMPort))
	wcs.config.userConfig.Set("baud_rate", requestData.BaudRate)
	wcs.config.userConfig.Set("noise_reduction", requestData.NoiseReduction)

	// Write to file
	if err := wcs.config.userConfig.WriteConfig(); err != nil {
		wcs.logger.Errorw("Failed to save configuration", "error", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// handleGetTargets returns available audio targets as JSON
func (wcs *WebConfigServer) handleGetTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	refresh := r.URL.Query().Get("refresh")
	if refresh == "1" {
		// Force session map refresh for running processes
		if wcs.deej.sessions != nil {
			wcs.deej.sessions.refreshSessions(true)
		}
	}

	targets, err := wcs.deej.GetAvailableAudioTargets()
	if err != nil {
		wcs.logger.Errorw("Failed to get available audio targets", "error", err)
		http.Error(w, "Failed to get audio targets", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(targets)
}
