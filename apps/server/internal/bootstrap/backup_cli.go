package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"tripfolio/server/internal/config"
	"tripfolio/server/internal/modules/backup"
)

func RunBackupCLI(args []string, out io.Writer) error {
	if len(args) == 1 && args[0] == "keygen" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, base64.StdEncoding.EncodeToString(key))
		return err
	}
	if len(args) == 0 || args[0] != "decrypt" {
		return errors.New("用法：tripfolio backup keygen | decrypt --input 备份.dump.age --output 备份.dump")
	}
	flags := flag.NewFlagSet("backup decrypt", flag.ContinueOnError)
	flags.SetOutput(out)
	input := flags.String("input", "", "加密备份路径")
	output := flags.String("output", "", "解密输出路径")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *input == "" || *output == "" || flags.NArg() != 0 {
		return errors.New("请指定 --input 和 --output")
	}
	if _, err := config.LoadDotEnv(os.Getenv); err != nil {
		return errors.New("无法读取备份密码配置")
	}
	in, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer in.Close()
	file, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	password := os.Getenv("TRIPFOLIO_BACKUP_PASSWORD")
	if password == "" {
		password = os.Getenv("TRIPFOLIO_BACKUP_KEY")
	}
	err = backup.Decrypt(password, in, file)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(*output)
		return err
	}
	_, err = fmt.Fprintln(out, "解密及文件完整性校验完成")
	return err
}
