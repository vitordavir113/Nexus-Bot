package framework

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/bwmarrin/discordgo"
)

const (
	CHANNELS   int = 2
	FRAME_RATE int = 48000
	FRAME_SIZE int = 960
	MAX_BYTES  int = (FRAME_SIZE * 2) * 2
)

// Função para enviar PCM para o Discord após passar pelo FFmpeg
func (connection *Connection) sendPCM(voice *discordgo.VoiceConnection, pcm <-chan []int16) {
	connection.lock.Lock()
	if connection.sendpcm || pcm == nil {
		connection.lock.Unlock()
		return
	}
	connection.sendpcm = true
	connection.lock.Unlock()
	defer func() {
		connection.sendpcm = false
	}()

	// Criando o comando FFmpeg para codificar PCM para Opus
	ffmpegCmd := exec.Command("ffmpeg", "-f", "s16le", "-ar", fmt.Sprintf("%d", FRAME_RATE), "-ac", fmt.Sprintf("%d", CHANNELS), "-i", "pipe:0", "-c:a", "libopus", "-b:a", "96k", "-f", "opus", "pipe:1")
	ffmpegStdin, err := ffmpegCmd.StdinPipe() // Usando StdinPipe() para obter um io.Writer
	if err != nil {
		fmt.Println("Erro ao criar pipe do FFmpeg:", err)
		return
	}

	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		fmt.Println("Erro ao criar pipe do FFmpeg:", err)
		return
	}

	err = ffmpegCmd.Start()
	if err != nil {
		fmt.Println("Erro ao iniciar FFmpeg:", err)
		return
	}

	go func() {
		for pcmData := range pcm {
			pcmBytes := make([]byte, len(pcmData)*2)
			for i, sample := range pcmData {
				pcmBytes[i*2] = byte(sample & 0xFF)          // LSB
				pcmBytes[i*2+1] = byte((sample >> 8) & 0xFF) // MSB
			}
			_, err := ffmpegStdin.Write(pcmBytes)
			if err != nil {
				fmt.Println("Erro ao enviar PCM para FFmpeg:", err)
				return
			}
		}
	}()

	buffer := bufio.NewReader(ffmpegStdout)
	for {
		opusPacket := make([]byte, MAX_BYTES)
		_, err := buffer.Read(opusPacket)
		if err == io.EOF {
			return
		}
		if err != nil {
			fmt.Println("Erro ao ler dados do FFmpeg:", err)
			return
		}
		if voice.Ready && voice.OpusSend != nil {
			voice.OpusSend <- opusPacket
		}
	}
}

// Função para tocar música utilizando FFmpeg
func (connection *Connection) Play(ffmpeg *exec.Cmd) error {
	if connection.playing {
		return errors.New("já está tocando uma música")
	}
	connection.stopRunning = false
	out, err := ffmpeg.StdoutPipe()
	if err != nil {
		return err
	}
	buffer := bufio.NewReaderSize(out, 16384)
	err = ffmpeg.Start()
	if err != nil {
		return err
	}
	connection.playing = true
	defer func() {
		connection.playing = false
	}()
	connection.voiceConnection.Speaking(true)
	defer connection.voiceConnection.Speaking(false)
	if connection.send == nil {
		connection.send = make(chan []int16, 2)
	}
	go connection.sendPCM(connection.voiceConnection, connection.send)
	for {
		if connection.stopRunning {
			ffmpeg.Process.Kill()
			break
		}
		audioBuffer := make([]int16, FRAME_SIZE*CHANNELS)
		err = binary.Read(buffer, binary.LittleEndian, &audioBuffer)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
		connection.send <- audioBuffer
	}
	return nil
}

// Função para parar a música
func (connection *Connection) Stop() {
	connection.stopRunning = true
	connection.playing = false
}
