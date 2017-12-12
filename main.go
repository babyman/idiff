package main

import (
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// A simple CLI tool that can compare 2 directories of images and output the differences in a 3rd using the ImageMagick compare tool.
func main() {

	threads := flag.Int("t", runtime.NumCPU(), "the number of concurrent pages to download")
	compare := flag.String("compare", "compare", "path to the ImageMagick compare command")

	flag.Parse()

	args := os.Args

	if len(args) != 4 {
		fmt.Println(os.Args[0], " [directory] [directory] [out dir]\n")
		flag.Usage()
		os.Exit(0)
	}

	dir1 := args[1]
	dir2 := args[2]
	outDir := args[3]

	os.MkdirAll(outDir, os.ModePerm)

	chIn := grabJobChannelGenerator(*compare, dir1, dir2, outDir)

	chFiltered := performDiffJobTask(filterDiffJobs, chIn)

	fmt.Println("Compairing images:")
	for n := range fanDiffJobsIn(fanOut(*threads, diffFiles, chFiltered)...) {
		fmt.Println("\t", n.outFile)
	}

}

func filterDiffJobs(job DiffJob) *DiffJob {
	if !fileExists(job.inFile1) || !fileExists(job.inFile2) {
		return nil
	}
	return &job
}

func diffFiles(job DiffJob) *DiffJob {
	compareFiles(job.comparePath, job.inFile1, job.inFile2, job.outFile)
	return &job
}

// -------------------------------------------------------------------------------------------------------------------------------------------------------------

type DiffJob struct {
	inFile1     string
	inFile2     string
	outFile     string
	comparePath string
}

// return a pointer so that nil is a valid result, this allows the filter task to work correctly
type DiffJobTask func(DiffJob) *DiffJob

func grabJobChannelGenerator(comparePath, inDir1, indDir2, outDir string) <-chan DiffJob {
	chOut := make(chan DiffJob)
	go func(inDir1, indDir2, outDir string) {
		files, err := ioutil.ReadDir(inDir1)

		if err != nil {
			log.Fatal(err)
		}

		for _, file := range files {
			if filepath.Ext(file.Name()) == ".png" {
				file1 := filepath.Join(inDir1, file.Name())
				file2 := filepath.Join(indDir2, file.Name())
				outFile := filepath.Join(outDir, file.Name())

				chOut <- DiffJob{file1, file2, outFile, comparePath}
			}
		}

		close(chOut)
	}(inDir1, indDir2, outDir)
	return chOut
}

func fanOut(count int, task DiffJobTask, chIn <-chan DiffJob) []<-chan DiffJob {

	var chFanned []<-chan DiffJob

	for i := 0; i < count; i++ {
		chFanned = append(chFanned, performDiffJobTask(task, chIn))
	}

	return chFanned
}

func performDiffJobTask(task DiffJobTask, chIn <-chan DiffJob) <-chan DiffJob {
	chOut := make(chan DiffJob)
	go func() {
		for grabJob := range chIn {
			if job := task(grabJob); job != nil {
				chOut <- *job
			}
		}
		close(chOut)
	}()
	return chOut
}

func fanDiffJobsIn(chIns ...<-chan DiffJob) <-chan DiffJob {
	chOut := make(chan DiffJob)

	var wg sync.WaitGroup
	wg.Add(len(chIns))

	go func() {
		for _, v := range chIns {
			go func(chIn <-chan DiffJob) {
				for i := range chIn {
					chOut <- i
				}
				wg.Done()
			}(v)
		}
	}()

	go func() {
		wg.Wait()
		close(chOut)
	}()

	return chOut
}

// -------------------------------------------------------------------------------------------------------------------------------------------------------------

func fileExists(path string) bool {
	if info, err := os.Stat(path); err != nil {
		return false
	} else if info.IsDir() {
		return false
	}
	return true
}

func compareFiles(comparePath, in1, in2, outFile string) {

	resize := outFile + "tmp"

	// resize the 2 images if necessary
	inA, inB := commonSizeImageLengths(in1, in2, resize)

	// compare the 2 images and generate the diff image file
	cmd := fmt.Sprintf("%s %s %s -highlight-color blue %s", comparePath, inA, inB, outFile)
	_, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		//fmt.Println(out)
		//fmt.Println(err)
	}

	// load the out file image
	img1, _ := loadAndDecodePng(inA)
	img2, _ := loadAndDecodePng(inB)
	imgDiff, _ := loadAndDecodePng(outFile)

	// combine them into a single image for comparison
	combineImages(img1, imgDiff, img2, outFile)

	// remove the outfile
	os.Remove(resize)

}

// compare the Y length of 2 images and resize the smaller one returning the file paths for the 2 files
func commonSizeImageLengths(in1, in2, out string) (string, string) {
	img1, _ := loadAndDecodePng(in1)
	img2, _ := loadAndDecodePng(in2)

	if img1.Bounds().Max.Y > img2.Bounds().Max.Y {
		// resize img2 since it is shorter
		resizeImage(img2, img1.Bounds(), out)
		return in1, out
	} else if img2.Bounds().Max.Y > img1.Bounds().Max.Y {
		// resize img1 since it is shorter
		resizeImage(img1, img2.Bounds(), out)
		return out, in2
	}
	return in1, in2
}

func resizeImage(img1 image.Image, size image.Rectangle, outputFile string) {
	newImage := image.NewRGBA(size)
	draw.Draw(newImage, img1.Bounds(), img1, image.Point{0, 0}, draw.Src)

	toImg, _ := os.Create(outputFile)
	defer toImg.Close()
	png.Encode(toImg, newImage)
}

// combine 3 images side by side
func combineImages(img1, img2, img3 image.Image, outputFile string) {

	width := img1.Bounds().Max.X + img2.Bounds().Max.X + img3.Bounds().Max.X
	height := intMax(intMax(img1.Bounds().Max.Y, img2.Bounds().Max.Y), img3.Bounds().Max.Y)

	image2Offset := image.Point{X: img1.Bounds().Max.X}
	image3Offset := image.Point{X: img1.Bounds().Max.X + img2.Bounds().Max.X}

	newImage := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(newImage, img1.Bounds(), img1, image.Point{0, 0}, draw.Src)
	draw.Draw(newImage, img2.Bounds().Add(image2Offset), img2, image.Point{0, 0}, draw.Src)
	draw.Draw(newImage, img3.Bounds().Add(image3Offset), img3, image.Point{0, 0}, draw.Src)

	toImg, _ := os.Create(outputFile)
	defer toImg.Close()
	png.Encode(toImg, newImage)
}

// load a file and decode it into an image object
func loadAndDecodePng(filePath string) (image.Image, error) {

	imageFile, e := os.Open(filePath)
	defer imageFile.Close()
	if e != nil {
		return nil, e
	}

	decodedImage, e := png.Decode(imageFile)
	if e != nil {
		return nil, e
	}

	return decodedImage, nil
}

// int max implementation!
func intMax(i1, i2 int) int {
	if i1 > i2 {
		return i1
	}
	return i2
}
