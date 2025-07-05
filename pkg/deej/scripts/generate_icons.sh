#!/bin/bash

# Check if 2goarray is available
if ! command -v 2goarray &> /dev/null; then
    echo "2goarray not found, installing..."
    go install github.com/cratonica/2goarray@latest
    
    # Add GOPATH/bin to PATH
    if [ -z "$GOPATH" ]; then
        GOPATH="$HOME/go"
    fi
    export PATH="$GOPATH/bin:$PATH"
    echo "2goarray installed successfully!"
fi

# Change to the icon directory
cd ../icon

# Generate the icon files
echo "Generating icon files..."
2goarray NormalLightIcon icon < ../assets/logo-normal-light.png > icon_normal_light.go
2goarray NormalDarkIcon icon < ../assets/logo-normal-dark.png > icon_normal_dark.go
2goarray ErrorLightIcon icon < ../assets/logo-error-light.png > icon_error_light.go
2goarray ErrorDarkIcon icon < ../assets/logo-error-dark.png > icon_error_dark.go

echo "Icon generation completed successfully!" 