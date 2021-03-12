// Command publish-go-lambda builds Go source in the current directory and
// publishes it as an existing AWS Lambda (Go 1.x runtime).
package main

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

func main() {
	log.SetFlags(0)
	var relaxedChecks bool
	flag.BoolVar(&relaxedChecks, "f", relaxedChecks, "skip some safety checks")
	flag.Parse()
	if err := run(context.Background(), flag.Arg(0), relaxedChecks); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, name string, relaxedChecks bool) error {
	if name == "" {
		return errors.New("name must be set")
	}
	shortName := name[strings.LastIndexByte(name, ':')+1:]
	if err := checkMainPackage(".", shortName, !relaxedChecks); err != nil {
		return err
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	svc := lambda.NewFromConfig(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfgOutput, err := svc.GetFunctionConfiguration(ctx, &lambda.GetFunctionConfigurationInput{
		FunctionName: &name,
		Qualifier:    aws.String("$LATEST"),
	})
	if err != nil {
		return fmt.Errorf("GetFunctionConfiguration: %w", err)
	}
	if cfgOutput.Runtime != types.RuntimeGo1x {
		return fmt.Errorf("lambda configured with unsupported runtime, want %s", types.RuntimeGo1x)
	}
	if cfgOutput.Handler == nil || *cfgOutput.Handler == "" {
		return errors.New("lambda configuration has empty handler name")
	}
	zipData, err := buildAndZip(".", *cfgOutput.Handler)
	if err != nil {
		return err
	}
	ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	_, err = svc.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: &name,
		RevisionId:   cfgOutput.RevisionId,
		ZipFile:      zipData,
		Publish:      true,
	})
	return err
}

func buildAndZip(dir, handlerName string) ([]byte, error) {
	tdir, err := ioutil.TempDir("", "publish-go-lambda-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tdir)
	binPath := filepath.Join(tdir, "main")
	cmd := exec.Command("go", "build", "-ldflags=-s -w", "-trimpath",
		"-o", binPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	f, err := os.Open(binPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	header, err := zip.FileInfoHeader(fi)
	if err != nil {
		return nil, err
	}
	header.Method = zip.Deflate
	header.Name = handlerName
	header.SetMode(0775)

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})
	w, err := zw.CreateHeader(header)
	if err != nil {
		return nil, err
	}
	begin := time.Now()
	if _, err := io.Copy(w, f); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	log.Printf("compressed from %.1fM to %.1fM in %v, compression ratio: %.2f",
		float64(header.UncompressedSize64/1024)/1024,
		float64(header.CompressedSize64/1024)/1024,
		time.Since(begin).Round(time.Millisecond),
		float64(header.CompressedSize64)/float64(header.UncompressedSize64))
	return buf.Bytes(), nil
}

func checkMainPackage(dir, lambdaName string, strict bool) error {
	if lambdaName == "" {
		panic("checkMainPackage called with an empty lambdaName")
	}
	fset := token.NewFileSet()
	mode := parser.ParseComments
	if !strict {
		mode = parser.ImportsOnly
	}
	pkgs, err := parser.ParseDir(fset, dir, nil, mode)
	if err != nil {
		return err
	}
	pkg, ok := pkgs["main"]
	if !ok {
		return fmt.Errorf("cannot find main package")
	}
	if !strict {
		return nil
	}
	nameRegex := regexp.MustCompile(`\b` + regexp.QuoteMeta(lambdaName) + `\b`)
	const awsDependency = `"github.com/aws/aws-lambda-go/lambda"`
	var hasLambdaImport bool
	var mentionsLambdaName bool
	for _, f := range pkg.Files {
		if !mentionsLambdaName && f.Doc != nil {
			mentionsLambdaName = nameRegex.MatchString(f.Doc.Text())
		}
		if !hasLambdaImport {
			for _, s := range f.Imports {
				if s.Path.Value == awsDependency {
					hasLambdaImport = true
					break
				}
			}
		}
	}
	if !mentionsLambdaName {
		return fmt.Errorf("package docs does not mention name %q (run with -f to skip this check)", lambdaName)
	}
	if !hasLambdaImport {
		return fmt.Errorf("package does not import %s dependency (run with -f to skip this check)", awsDependency)
	}
	return nil
}

func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s aws-lambda-name\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(flag.CommandLine.Output(), "\naws-lambda-name is either a short AWS Lambda name, or a fully qualified ARN\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
}
