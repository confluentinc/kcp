#!/bin/bash

# Function to cleanup background processes
cleanup() {
    echo "Cleaning up background processes..."
    # Kill background yarn process if it exists
    if [ ! -z "$YARN_PID" ]; then
        kill $YARN_PID 2>/dev/null
    fi
    # Kill air process if it exists
    if [ ! -z "$AIR_PID" ]; then
        kill $AIR_PID 2>/dev/null
    fi
    echo "Cleanup complete!"
    exit 0
}

# Set up signal handlers
trap cleanup SIGINT SIGTERM

echo "Choose development mode:"
echo "1) Frontend only (yarn dev)"
echo "2) Frontend + Backend (yarn dev & air)"
echo ""
read -p "Enter your choice (1 or 2): " choice

case $choice in
    1)
        echo "Starting frontend only..."
        yarn dev
        ;;
    2)
        echo "Starting frontend + backend..."
        echo "Starting yarn devfebe in background..."
        yarn devfebe &
        YARN_PID=$!
        echo "Yarn PID: $YARN_PID"
        echo "Starting air..."
        air &
        AIR_PID=$!
        echo "Air PID: $AIR_PID"
        echo "Both processes started. Press Ctrl+C to stop both."
        # Wait for both processes
        wait
        ;;
    *)
        echo "Invalid choice. Please run the script again and select 1 or 2."
        exit 1
        ;;
esac 