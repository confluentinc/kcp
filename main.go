package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "kcp",
	Short: "A basic CLI built with Cobra",
	Long:  `A basic CLI application built with Cobra that demonstrates basic command functionality.`,
}

var helloCmd = &cobra.Command{
	Use:   "hello",
	Short: "Returns hello world",
	Long:  `A simple command that returns hello world.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("hello world")
	},
}

func init() {
	rootCmd.AddCommand(helloCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
