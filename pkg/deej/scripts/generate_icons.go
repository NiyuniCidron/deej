package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	// Check if 2goarray is available
	if _, err := exec.LookPath("2goarray"); err != nil {
		// 2goarray not found, try to install it
		fmt.Println("2goarray not found, installing...")
		installCmd := exec.Command("go", "install", "github.com/cratonica/2goarray@latest")
		installCmd.Stdout = os.Stdout
		installCmd.Stderr = os.Stderr
		if err := installCmd.Run(); err != nil {
			fmt.Printf("Failed to install 2goarray: %v\n", err)
			os.Exit(1)
		}

		// Add GOPATH/bin to PATH
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			home, _ := os.UserHomeDir()
			gopath = filepath.Join(home, "go")
		}
		gobin := filepath.Join(gopath, "bin")

		// Update PATH for this process
		currentPath := os.Getenv("PATH")
		os.Setenv("PATH", gobin+string(os.PathListSeparator)+currentPath)

		fmt.Println("2goarray installed successfully!")
	}

	// Change to the icon directory
	if err := os.Chdir("../icon"); err != nil {
		fmt.Printf("Failed to change to icon directory: %v\n", err)
		os.Exit(1)
	}

	// Now run the 2goarray commands
	commands := []string{
		"2goarray NormalLightIcon icon < ../assets/logo-normal-light.png > icon_normal_light.go",
		"2goarray NormalDarkIcon icon < ../assets/logo-normal-dark.png > icon_normal_dark.go",
		"2goarray ErrorLightIcon icon < ../assets/logo-error-light.png > icon_error_light.go",
		"2goarray ErrorDarkIcon icon < ../assets/logo-error-dark.png > icon_error_dark.go",
	}

	for _, cmd := range commands {
		fmt.Printf("Running: %s\n", cmd)
		execCmd := exec.Command("sh", "-c", cmd)
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		if err := execCmd.Run(); err != nil {
			fmt.Printf("Failed to run: %s\n", cmd)
			os.Exit(1)
		}
	}

	fmt.Println("Icon generation completed successfully!")
}
