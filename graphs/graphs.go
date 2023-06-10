// Utility for generating gnuplot graphs of histosketch sums and quantiles.
// go build in this directory and then pipe the output to gnuplot to generate
// a png graph at /tmp/plot.png:
//
//   graphs --dist=normal --centroids=8 | gnuplot
//
// You can adjust the number of samples or the distribution used:
//
//   graphs --dist=uniform --centroids=8 --samples=50000 | gnuplot
//
// Instead of graphing a distribution, you can prepare text file of data, one
// float64 per line and use that instead:
//
//   graphs --datafile=/tmp/my_data.txt --centroids=8 | gnuplot
//
// If you want to bootstrap the sketch with the optimal centroid decomposition
// for the first 1000 samples, use the `bootstrap` flag:
//
//   graphs --dist=exponential --centroids=8 --samples=50000 --bootstrap=1000
//
// Run with "--help" flag for more information.

package main

import (
	"github.com/aaw/histosketch"
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"time"
)

type statFn func(float64) float64
type distFn func() float64

// Reads a file with one float64 per line, returns the floats one at a time via the
// returned channel.
func fileReader(filename string) chan float64 {
	yield := make (chan float64)
	f, err := os.Open(filename)
	if err != nil {
		panic(fmt.Sprintf("Error opening file: %v", err))
	}
	scanner := bufio.NewScanner(f)
	scanner.Split(bufio.ScanLines)
	go func () {
		for scanner.Scan() {
			val, verr := strconv.ParseFloat(scanner.Text(), 64)
			if verr != nil {
				panic(fmt.Sprintf("Error parsing float '%v': %v", scanner.Text(), verr))
			}
			yield <- val
		}
		close(yield)
		f.Close()
	}()
	return yield
}

func distReader(f distFn, n int) chan float64 {
	yield := make (chan float64)
	go func () {
		for i := 0; i < n; i++ {
			yield <- f()
		}
		close(yield)
	}()
	return yield
}

// Counts the number of lines in a file.
func lineCount(filename string) int {
	f, err := os.Open(filename)
	if err != nil {
		panic(fmt.Sprintf("Error opening file: %v", err))
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Split(bufio.ScanLines)
	count := 0
	for scanner.Scan() {
		count += 1
	}
	return count
}

func plotComparison(s1 statFn, s2 statFn, begin float64, end float64, step float64) {
	f, _ := os.Create("/tmp/plot.dat")
	defer f.Close()
	k := begin
	for ; k <= end; k += step {
		f.WriteString(fmt.Sprintf("%v %v %v\n", k, s1(k), s2(k)))
	}
	if k - end < step {
		f.WriteString(fmt.Sprintf("%v %v %v\n", end, s1(end), s2(end)))
	}
}

func main() {
	dist := flag.String("dist", "uniform", "Distribution to use: 'uniform', 'normal', 'exponential'")
	plot := flag.String("plot", "quantile", "Type of plot: 'quantile' or 'sum'")
	samples := flag.Int("samples", 10000, "Number of samples to add to histogram. Also, size of the exact histogram.")
	centroids := flag.Int("centroids", 10, "Number of centroids in the sketch")
	step := flag.Float64("step", 0.01, "Step size of the resulting plot")
	seed := flag.Int64("seed", 0, "Seed for random number generator (0 to use current time).")
	datafile := flag.String("datafile", "", "File containing one floating point value per line (overrides dist setting).")
	bootstrap := flag.Int("bootstrap", 0, "Bootstrap the sketch with an optimal centroid decomposition from this many of the samples")
	flag.Parse()

	ss := *seed
	if *seed == 0 && *datafile == "" {
		ss = time.Now().UnixNano()
		fmt.Fprintln(os.Stderr, fmt.Sprintf("# Seed: %v\n", ss))
	}
	rand.Seed(ss)

	h1 := histosketch.New(uint(*centroids))
	h2 := histosketch.New(uint(*samples))

	var reader chan float64
	if *datafile != "" {
		reader = fileReader(*datafile)
		*samples = lineCount(*datafile)
	} else if *dist == "uniform" {
		reader = distReader(rand.Float64, *samples)
	} else if *dist == "normal" {
		reader = distReader(rand.NormFloat64, *samples)
	} else if *dist == "exponential" {
		reader = distReader(rand.ExpFloat64, *samples)
	} else {
		fmt.Printf("Unknown distribution: %v\n", *dist)
		return
	}
	if *bootstrap > *samples {
		*bootstrap = *samples
	}

	sample := []float64{}
	for i := 0; i < *bootstrap; i++ {
		val := <-reader
		sample = append(sample, val)
		h2.Add(val)
	}

	if *bootstrap > 0 {
		h1 = histosketch.NewFromSample(sample, *centroids)
		fmt.Fprintln(os.Stderr, fmt.Sprintf("# H: %v\n", h1))
	}

	for x := range reader {
		h1.Add(x)
		h2.Add(x)
	}

	var s1, s2 statFn
	var begin, end float64
	if *plot == "quantile" {
		s1, s2 = h1.Quantile, h2.Quantile
		begin, end = 0.0, 1.0
	} else if *plot == "sum" {
		s1, s2 = h1.Sum, h2.Sum
		begin, end = h1.Min(), h1.Max()
	} else {
		fmt.Printf("Unknown plot type: %v\n", *plot)
		return
	}

	plotComparison(s1, s2, begin, end, *step)

	ssb := int(*centroids) * 256 + 192
	sd := ""
	if ssb > 1024 * 1024 {
		sd = fmt.Sprintf("%.1f MB", float64(ssb) / 1024.0 / 1024.0)
	} else {
		sd = fmt.Sprintf("%.1f KB", float64(ssb) / 1024.0)
	}

	fmt.Println("set term png")
	fmt.Println("set output '/tmp/plot.png'")
	if *datafile == "" {
		fmt.Printf("set title \"%v distribution, %v samples\\n", *dist, *samples)
	} else {
		fmt.Printf("set title \"%v\\n", *datafile)
	}
	fmt.Printf("sketch with %v centroids (~%v)\"\n", *centroids, sd)
	fmt.Println("set xlabel \"x\"")
	fmt.Printf("set ylabel \"%v(x)\"\n", *plot)

	// Next six lines make the graph axes and grid lines look nice. Stolen from Hagen Wierstorf
	// at www.gnuplotting.org/code/xyborder.cfg and www.gnuplotting.org/code/grid.cfg.
	fmt.Println("set style line 101 lc rgb '#808080' lt 1 lw 1")
	fmt.Println("set border 3 front ls 101")
	fmt.Println("set tics nomirror out scale 0.75")
	fmt.Println("set format '%g'")
	fmt.Println("set style line 102 lc rgb '#d6d7d9' lt 0 lw 1")
	fmt.Println("set grid back ls 102")

	if *plot == "quantile" {
		fmt.Println("set key top left")
	} else {
		fmt.Println("set key bottom right")
	}
	fmt.Printf("plot ")
	fmt.Printf("'/tmp/plot.dat' using 1:2:3 title \"Sketch error\" with filledcurves lc rgb \"#E7298A\", ")
	fmt.Printf("'/tmp/plot.dat' using 1:3 title \"Actual\" with lines lc rgb \"blue\"\n")
}
