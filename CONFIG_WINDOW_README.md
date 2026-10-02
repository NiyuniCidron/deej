# deej Configuration Window

This document describes the new configuration window feature added to deej.

## Overview

The configuration window provides a user-friendly, platform-agnostic way to edit deej's configuration without manually editing the `config.yaml` file. It's implemented as a web-based interface that runs locally on your machine.

## Features

### User-Friendly Interface
- **Visual Configuration**: No need to manually edit YAML files
- **Real-time Validation**: Input validation and error handling
- **Modern UI**: Clean, responsive web interface
- **Cross-platform**: Works on Windows, Linux, and macOS

### Easy Slider Mapping
- **Special Targets**: Quick selection of special targets like `master`, `mic`, `deej.unmapped`, etc.
- **Common Applications**: Pre-populated list of common applications
- **Multiple Targets**: Support for multiple applications per slider (comma-separated)
- **Visual Organization**: Clear layout with 10 slider slots (0-9)

### Connection Settings
- **COM Port Configuration**: Easy COM port selection with auto-detection support
- **Baud Rate**: Configurable baud rate (default: 9600)
- **Validation**: Input validation for connection parameters

### Other Settings
- **Invert Sliders**: Toggle for inverted slider behavior
- **Noise Reduction**: Dropdown selection for hardware quality settings

## How to Use

1. **Launch deej**: Start the deej application
2. **Access Configuration**: Right-click the deej tray icon
3. **Open Configuration Window**: Select "Configuration Window" from the menu
4. **Configure Settings**: Use the web interface to modify your settings
5. **Save Changes**: Click "Save Configuration" to apply changes
6. **Automatic Reload**: Changes are applied immediately without restarting deej

## Technical Details

### Web Server
- **Port**: Runs on `localhost:8080`
- **Protocol**: HTTP/HTTPS not required (local only)
- **Security**: No external network access
- **Browser**: Opens in your default web browser

### Configuration Persistence
- **File Format**: Still uses YAML format for `config.yaml`
- **Auto-reload**: deej automatically detects and applies configuration changes
- **Backup**: Original `config.yaml` file is preserved and updated

### Platform Support
- **Windows**: Uses `start` command to open browser
- **Linux**: Uses `xdg-open` command to open browser
- **macOS**: Compatible with standard web browsers

## File Structure

```
pkg/deej/
├── web_config.go          # Web-based configuration server
├── tray.go               # Updated tray menu with config window option
└── config.go             # Existing configuration management
```

## Benefits Over Manual Editing

1. **No YAML Syntax Errors**: Eliminates common YAML formatting mistakes
2. **Visual Feedback**: Immediate validation and error messages
3. **Easier Discovery**: Special targets and common apps are readily available
4. **Consistent Formatting**: Automatic proper formatting of configuration
5. **User Guidance**: Helpful tooltips and descriptions

## Troubleshooting

### Web Interface Not Opening
- Check if port 8080 is available
- Ensure your default browser is properly configured
- Check deej logs for any server errors

### Configuration Not Saving
- Verify write permissions for the `config.yaml` file
- Check that the web interface shows success message
- Review deej logs for any file write errors

### Browser Compatibility
- Works with all modern browsers (Chrome, Firefox, Safari, Edge)
- Requires JavaScript enabled
- No internet connection required

## Future Enhancements

Potential improvements for the configuration window:
- **Process Discovery**: Automatic detection of running applications
- **Device Selection**: Visual selection of audio devices
- **Profile Management**: Save and load different configuration profiles
- **Advanced Settings**: Additional configuration options
- **Theme Support**: Dark/light theme options

## Security Considerations

- **Local Only**: The web server only listens on localhost
- **No Authentication**: Not required for local-only access
- **Temporary**: Server stops when deej is closed
- **No Data Collection**: All configuration remains local

## Migration from Manual Editing

Existing `config.yaml` files are fully compatible with the new configuration window. The web interface will load and display your current configuration, and you can continue using either method to edit settings. 