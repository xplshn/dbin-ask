package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	icons "fyne.io/fyne/v2/theme"

	"fyne.io/x/fyne/theme"
	//"fyne.io/x/fyne/layout"
)

const (
	AppVersion           = "1.0.0"
	ResourceTypeIcon     = "icon"
	ResourceTypeScreenshot = "screenshot"
	paddingSize          = 16
	windowWidth          = 700
	windowHeight         = 600
	iconSize             = 80
	screenshotWidth      = 600
	screenshotHeight     = 300
)

type Resource struct {
	Type string
	URL  string
	Path string
}

type ResourceManager struct {
	tempDir   string
	resources []Resource
}

func NewResourceManager(programID string) (*ResourceManager, error) {
	uniqueID := BinaryIDString(programID)
	tempDir := filepath.Join(os.TempDir(), uniqueID)
	if err := os.MkdirAll(tempDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	rm := &ResourceManager{
		tempDir:   tempDir,
		resources: make([]Resource, 0),
	}

	setupCleanupSignalHandler(rm)
	return rm, nil
}

func BinaryIDString(programID string) string {
	idHash := sha256.Sum256([]byte(programID))
	return "dbinAsk-" + hex.EncodeToString(idHash[:8])
}

func setupCleanupSignalHandler(rm *ResourceManager) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		rm.Cleanup()
		os.Exit(1)
	}()
}

func (rm *ResourceManager) Cleanup() {
	os.RemoveAll(rm.tempDir)
}

func (rm *ResourceManager) DownloadResource(url, resourceType string) (string, error) {
	urlHash := sha256.Sum256([]byte(url))
	fileName := fmt.Sprintf("%s-%s%s",
		resourceType,
		hex.EncodeToString(urlHash[:4]),
		filepath.Ext(url))

	filePath := filepath.Join(rm.tempDir, fileName)

	for _, res := range rm.resources {
		if res.URL == url {
			return res.Path, nil
		}
	}

	file, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("error creating file: %w", err)
	}
	defer file.Close()

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("error downloading %s: %w", resourceType, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error downloading %s: status %d", resourceType, resp.StatusCode)
	}

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", fmt.Errorf("error writing %s: %w", resourceType, err)
	}

	rm.resources = append(rm.resources, Resource{
		Type: resourceType,
		URL:  url,
		Path: filePath,
	})

	return filePath, nil
}

func FormatBinaryID(name, pkgID string) string {
	if pkgID == "" {
		return name
	}
	return name + "#" + pkgID
}

type UI struct {
	app         fyne.App
	window      fyne.Window
	resources   *ResourceManager
	info        *binaryEntry
	program     string
	iconImage   *canvas.Image
	screenshots []*canvas.Image
}

func NewUI(program string, info binaryEntry) (*UI, error) {
	resources, err := NewResourceManager(FormatBinaryID(info.Name, info.PkgId))
	if err != nil {
		return nil, err
	}

	a := app.New()
	a.Settings().SetTheme(theme.AdwaitaTheme())
	w := a.NewWindow(fmt.Sprintf("Install %s", FormatBinaryID(info.Name, info.PkgId)))
	w.Resize(fyne.NewSize(windowWidth, windowHeight))

	ui := &UI{
		app:         a,
		window:      w,
		resources:   resources,
		info:        &info,
		program:     program,
		screenshots: make([]*canvas.Image, 0),
	}

	a.Lifecycle().SetOnStopped(resources.Cleanup)
	return ui, nil
}

func (ui *UI) LoadIcon() error {
	if ui.info.Icon == "" {
		// Use default icon if none provided
		ui.iconImage = canvas.NewImageFromResource(icons.FyneLogo())
		ui.iconImage.FillMode = canvas.ImageFillContain
		ui.iconImage.SetMinSize(fyne.NewSize(iconSize, iconSize))
		return nil
	}

	iconPath, err := ui.resources.DownloadResource(ui.info.Icon, ResourceTypeIcon)
	if err != nil {
		return err
	}

	res, _ := fyne.LoadResourceFromPath(iconPath)
	ui.window.SetIcon(res)
	ui.iconImage = canvas.NewImageFromResource(res)
	ui.iconImage.FillMode = canvas.ImageFillContain
	ui.iconImage.SetMinSize(fyne.NewSize(iconSize, iconSize))
	return nil
}

func (ui *UI) Initialize() error {
	if err := ui.LoadIcon(); err != nil {
		log.Printf("Warning: failed to load icon: %v", err)
	}

	if err := ui.LoadScreenshots(); err != nil {
		log.Printf("Warning: failed to load screenshots: %v", err)
	}

	return nil
}

func (ui *UI) LoadScreenshots() error {
	ui.screenshots = make([]*canvas.Image, 0, len(ui.info.Screenshots))

	for _, url := range ui.info.Screenshots {
		path, err := ui.resources.DownloadResource(url, ResourceTypeScreenshot)
		if err != nil {
			log.Printf("Warning: failed to download screenshot %s: %v", url, err)
			continue
		}

		img := canvas.NewImageFromFile(path)
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(screenshotWidth, screenshotHeight))
		ui.screenshots = append(ui.screenshots, img)
	}

	return nil
}

func (ui *UI) createHeader(title string) fyne.CanvasObject {
	titleLabel := widget.NewLabel(title)
	titleLabel.TextStyle.Bold = true
	titleLabel.Alignment = fyne.TextAlignCenter

	iconContainer := container.NewCenter(ui.iconImage)

	headerLayout := container.NewVBox(
		container.NewHBox(
			iconContainer,
			container.NewCenter(titleLabel),
		),
	)

	return headerLayout
}

func (ui *UI) createInfoTabs() fyne.CanvasObject {
	tabs := container.NewAppTabs(
		container.NewTabItem("DESCRIPTION", ui.CreateDescriptionContainer()),
		container.NewTabItem("DETAILS", ui.CreateMetadataContainer()),
		container.NewTabItem("NOTES", ui.CreateNotesContainer()),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	return tabs
}

// Don't use scroll containers here, let content expand naturally
func (ui *UI) CreateDescriptionContainer() fyne.CanvasObject {
	desc := ui.info.Description
	if desc == "" {
		desc = "No description available"
	}
	richText := widget.NewRichTextFromMarkdown(desc)
	richText.Wrapping = fyne.TextWrapWord

	return container.NewVBox(richText)
}

func (ui *UI) CreateNotesContainer() fyne.CanvasObject {
	if len(ui.info.Notes) == 0 {
		return widget.NewLabel("No notes available")
	}

	notesMD := "## Notes\n\n" + strings.Join(ui.info.Notes, "\n\n")
	richText := widget.NewRichTextFromMarkdown(notesMD)
	richText.Wrapping = fyne.TextWrapWord

	return container.NewVBox(richText)
}

func (ui *UI) CreateMetadataContainer() fyne.CanvasObject {
	var md strings.Builder
	md.WriteString("## Details\n\n")

	addField := func(label, value string) {
		if value != "" {
			md.WriteString(fmt.Sprintf("- **%s**: %s\n", label, value))
		}
	}

	addField("Version", ui.info.Version)
	addField("Size", ui.info.Size)
	addField("Build Date", ui.info.BuildDate)
	if len(ui.info.License) > 0 {
		addField("License", strings.Join(ui.info.License, ", "))
	}

	if md.Len() == len("## Details\n\n") {
		return widget.NewLabel("No metadata available")
	}

	details := widget.NewRichTextFromMarkdown(md.String())
	details.Wrapping = fyne.TextWrapWord

	return container.NewVBox(details)
}

func (ui *UI) createScreenshotsSection() fyne.CanvasObject {
	if len(ui.screenshots) == 0 {
		return widget.NewLabel("No screenshots available")
	}
	return ui.CreateScreenshotsCarousel()
}

func (ui *UI) createActionButtons() fyne.CanvasObject {
	installButton := widget.NewButton("Install", func() {
		ui.CreateInstallationScreen()
	})

	cancelButton := widget.NewButton("Cancel", func() {
		ui.app.Quit()
	})

	// Use a container that puts Install on left and Cancel on right
	return container.NewBorder(
		nil, nil,
		installButton,
		cancelButton,
		nil,
	)
}

func (ui *UI) CreateScreenshotsCarousel() fyne.CanvasObject {
	if len(ui.screenshots) == 0 {
		return container.NewCenter(widget.NewLabel("No screenshots available"))
	}

	currentIndex := 0
	imageContainer := container.NewMax(ui.screenshots[0])

	updateImage := func(index int) {
		if index < 0 {
			index = len(ui.screenshots) - 1
		} else if index >= len(ui.screenshots) {
			index = 0
		}

		currentIndex = index
		imageContainer.Objects = []fyne.CanvasObject{ui.screenshots[currentIndex]}
		imageContainer.Refresh()
	}

	prevButton := widget.NewButtonWithIcon("", icons.NavigateBackIcon(), func() {
		updateImage(currentIndex - 1)
	})
	prevButton.Importance = widget.MediumImportance

	nextButton := widget.NewButtonWithIcon("", icons.NavigateNextIcon(), func() {
		updateImage(currentIndex + 1)
	})
	nextButton.Importance = widget.MediumImportance

	counterLabel := widget.NewLabel(fmt.Sprintf("1/%d", len(ui.screenshots)))
	counterLabel.Alignment = fyne.TextAlignCenter

	originalUpdate := updateImage
	updateImage = func(index int) {
		originalUpdate(index)
		counterLabel.SetText(fmt.Sprintf("%d/%d", currentIndex+1, len(ui.screenshots)))
	}

	imageRow := container.NewBorder(
		nil, nil,
		prevButton,
		nextButton,
		container.NewCenter(imageContainer),
	)

	carouselContainer := container.NewVBox(
		imageRow,
		container.NewCenter(counterLabel),
	)

	return carouselContainer
}

func (ui *UI) CreateConfirmationScreen() {
	title := fmt.Sprintf("Install %s", FormatBinaryID(ui.info.Name, ui.info.PkgId))
	header := ui.createHeader(fmt.Sprintf("Do you wish to proceed with the installation process?"))
	screenshots := ui.createScreenshotsSection()
	tabs := ui.createInfoTabs()
	buttons := ui.createActionButtons()

	// Main content that expands to fill window
	mainContent := container.NewVBox(
		header,
		widget.NewSeparator(),
		screenshots,
		widget.NewSeparator(),
		tabs,
	)

	// Put it all together with buttons at bottom
	content := container.NewBorder(
		nil,
		buttons,
		nil, nil,
		container.NewScroll(mainContent), // Main content is scrollable as needed
	)

	ui.window.SetContent(content)
	ui.window.SetTitle(title)
}

func (ui *UI) Run() {
	ui.CreateConfirmationScreen()
	ui.window.ShowAndRun()
}

func (ui *UI) CreateInstallationScreen() {
	binaryName := FormatBinaryID(ui.info.Name, ui.info.PkgId)
	ui.window.SetTitle(fmt.Sprintf("Installing %s", binaryName))

	header := ui.createHeader(fmt.Sprintf("%s is currently being installed into your system", binaryName))

	progressDetails := widget.NewLabel("Preparing installation...")
	progressDetails.Alignment = fyne.TextAlignCenter

	progressBar := widget.NewProgressBar()
	progressBar.SetValue(0.0)

	var screenshotsContainer fyne.CanvasObject
	if len(ui.screenshots) > 0 {
		screenshotsContainer = ui.CreateScreenshotsCarousel()
	} else {
		placeholder := widget.NewLabel("No screenshots available")
		placeholder.Alignment = fyne.TextAlignCenter
		screenshotsContainer = container.NewCenter(placeholder)
	}

	progressSection := container.NewVBox(
		progressDetails,
		progressBar,
	)

	infoTabs := container.NewAppTabs(
		container.NewTabItem("DESCRIPTION", ui.CreateDescriptionContainer()),
		container.NewTabItem("DETAILS", ui.CreateMetadataContainer()),
		container.NewTabItem("NOTES", ui.CreateNotesContainer()),
	)
	infoTabs.SetTabLocation(container.TabLocationTop)

	// Create main content with proper layout
	mainContent := container.NewVBox(
		header,
		widget.NewSeparator(),
		screenshotsContainer,
		widget.NewSeparator(),
		infoTabs,
		progressSection,
	)

	// Use a scroll container for the entire content
	content := container.NewScroll(mainContent)

	ui.window.SetContent(content)
	go ui.RunInstallation(progressBar, progressDetails)
}

func (ui *UI) RunInstallation(progressBar *widget.ProgressBar, statusLabel *widget.Label) {
	binaryName := FormatBinaryID(ui.info.Name, ui.info.PkgId)
	statusLabel.SetText("Starting installation...")

	cmd := exec.Command("dbin", "install", ui.program)
	cmd.Env = append(os.Environ(), "DBIN_PB_FIFO=1")
	if err := cmd.Start(); err != nil {
		dialog.ShowError(fmt.Errorf("Failed to start installation: %w", err), ui.window)
		return
	}

	statusLabel.SetText("Installation in progress...")
	fifoPath := filepath.Join(os.TempDir(), "dbin", binaryName)

	fifoReady := make(chan bool, 1)
	go func() {
		for attempts := 0; attempts < 50; attempts++ {
			if _, err := os.Stat(fifoPath); err == nil {
				fifoReady <- true
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		fifoReady <- false
	}()

	select {
	case ready := <-fifoReady:
		if ready {
			statusLabel.SetText("Monitoring installation progress...")
			ui.monitorProgress(cmd, fifoPath, progressBar, statusLabel)
		} else {
			statusLabel.SetText("Progress monitoring unavailable, installation continuing...")
			dialog.ShowInformation("Notice",
				"Progress information not available. Installation is continuing.",
				ui.window)
			ui.waitForProcess(cmd, progressBar, statusLabel)
		}
	}
}

func (ui *UI) monitorProgress(cmd *exec.Cmd, fifoPath string, progressBar *widget.ProgressBar, statusLabel *widget.Label) {
	fifoFile, err := os.Open(fifoPath)
	if err != nil {
		statusLabel.SetText("Cannot monitor progress, waiting for installation to complete...")
		ui.waitForProcess(cmd, progressBar, statusLabel)
		return
	}
	defer fifoFile.Close()

	var lastPercentage float64 = -1
	scanner := bufio.NewScanner(fifoFile)
	for scanner.Scan() {
		line := scanner.Text()

		var percentage float64
		if _, err := fmt.Sscanf(line, "%f", &percentage); err == nil {
			if percentage == lastPercentage {
				continue
			}
			lastPercentage = percentage

			progressBar.SetValue(percentage / 100.0)
			statusLabel.SetText(fmt.Sprintf("Installing... %.1f%%", percentage))
		}
	}

	ui.waitForProcess(cmd, progressBar, statusLabel)
}

func (ui *UI) waitForProcess(cmd *exec.Cmd, progressBar *widget.ProgressBar, statusLabel *widget.Label) {
	err := cmd.Wait()

	if err != nil {
		progressBar.SetValue(1.0)
		statusLabel.SetText("Installation failed")
		dialog.ShowError(fmt.Errorf("Installation failed: %w", err), ui.window)
	} else {
		progressBar.SetValue(1.0)
		statusLabel.SetText("Installation completed successfully")

		successDialog := dialog.NewInformation("Installation Complete",
			"The package was installed successfully.", ui.window)
		successDialog.SetOnClosed(func() {
			ui.app.Quit()
		})
		successDialog.Show()
	}
}

func ParseInstallURI(uri string) (string, error) {
	if !strings.HasPrefix(uri, "dbin://ask/install/") {
		return "", errors.New("invalid URI format. Was expecting: dbin://ask/install/*")
	}

	parts := strings.Split(uri, "/")
	if len(parts) != 5 {
		return "", errors.New("invalid URI format")
	}

	programEncoded := parts[4]
	program, err := url.QueryUnescape(programEncoded)
	if err != nil {
		return "", fmt.Errorf("error decoding program: %w", err)
	}

	return program, nil
}

func GetProgramInfo(program string) (*binaryEntry, error) {
	var info binaryEntry

	cmd := exec.Command("dbin", "info", "--json", program)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error executing dbin info: %w", err)
	}

	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("error parsing JSON: %w", err)
	}

	return &info, nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: dbin-ask dbin://ask/install/program%23id")
		os.Exit(1)
	}

	uri := os.Args[1]
	programID, err := ParseInstallURI(uri)
	if err != nil {
		log.Fatalf("Error parsing URI: %v", err)
	}

	info, err := GetProgramInfo(programID)
	if err != nil {
		log.Fatalf("Error getting program info: %v", err)
	}

	ui, err := NewUI(programID, *info)
	if err != nil {
		log.Fatalf("Error creating UI: %v", err)
	}

	if err := ui.Initialize(); err != nil {
		log.Fatalf("Error initializing UI: %v", err)
	}

	ui.Run()
}
