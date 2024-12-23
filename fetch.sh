# Makefile をダウンロードする
MAKEFILE_URL="https://raw.githubusercontent.com/Yuhi-Sato/isucon-ready/main/Makefile"
MAKEFILE_PATH="./Makefile"
echo "Downloading Makefile from $MAKEFILE_URL..."
if wget -q -O "$MAKEFILE_PATH" "$MAKEFILE_URL"; then
  echo "Makefile downloaded successfully."
else
  echo "Failed to download Makefile. Exiting."
  exit 1
fi
