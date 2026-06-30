package config

import "testing"

func TestStorageConfig_DiskFields(t *testing.T) {
	c := &Storage{Type: "disk", PublicBaseURL: "https://cdn.example.com",
		Disk: &DiskStorageConfig{RootDir: "/data", BaseUrl: "cdn.example.com"}}
	if c.Disk.RootDir != "/data" || c.Disk.BaseUrl != "cdn.example.com" {
		t.Fatalf("unexpected disk config: %+v", c.Disk)
	}
	if c.Type != "disk" {
		t.Fatalf("unexpected type: %s", c.Type)
	}
}

func TestStorageConfig_OSSBaseDir(t *testing.T) {
	c := &Storage{Type: "oss", OSS: &OSSStorageConfig{Bucket: "b", Region: "cn-hangzhou", BaseDir: "prefix"}}
	if c.OSS.BaseDir != "prefix" {
		t.Fatalf("BaseDir not set: %+v", c.OSS)
	}
}
